package bundlereader

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/sirupsen/logrus"

	fleetssh "github.com/rancher/fleet/internal/ssh"
	fleetgit "github.com/rancher/fleet/pkg/git"
)

// gitDownload clones rawURL into dst using go-git.
//
// rawURL may include ?ref=, ?sshkey=, and ?depth= query parameters. The auth
// struct provides credentials, TLS settings, and optional SSH known-hosts.
//
// TLS verification uses the system cert pool augmented with auth.CABundle (if
// non-empty). PROXY_CA_BUNDLE is appended to auth.CABundle before passing to
// go-git, so that HTTPS repos cloned through an HTTPS proxy with a custom
// certificate are trusted while well-known public CAs remain accepted.
// For SSH repos via HTTPS proxy, the CONNECT tunnel is established by
// newHTTPConnectDialer in pkg/git/proxy.go, which likewise starts from the
// system cert pool and appends PROXY_CA_BUNDLE.
func gitDownload(ctx context.Context, dst, rawURL string, auth Auth) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing git URL %q: %w", rawURL, err)
	}

	ref, sshKeyPEM, depth, err := extractQueryParams(u)
	if err != nil {
		return err
	}

	// extractQueryParams already removed only the Fleet-specific params (?ref=,
	// ?sshkey=, ?depth=) from u.RawQuery. Copy the URL so any remaining query
	// params (e.g. signed-URL tokens) are preserved for the clone.
	cloneURL := *u

	d := depth
	if d == 0 {
		d = 1
	}

	// go-git applies CABundle per-clone via CloneOptions: it clones the base
	// transport in newSession and configures TLS on that copy without touching
	// the process-global gitclient.Protocols map. Concurrent HTTPS clones with
	// different CA bundles are therefore fully independent and need no mutex.
	//
	// Validate the PEM upfront: an invalid bundle would otherwise surface as a
	// generic TLS handshake failure, which is harder to diagnose.
	if len(auth.CABundle) > 0 && !auth.InsecureSkipVerify {
		if !x509.NewCertPool().AppendCertsFromPEM(auth.CABundle) {
			return errors.New("CA bundle contains no valid PEM certificates")
		}
	}

	// Merge PROXY_CA_BUNDLE so that HTTPS repos cloned through an HTTPS proxy
	// with a custom CA certificate are trusted. Make a defensive copy of
	// auth.CABundle so the caller's slice is never modified.
	caBundle := append([]byte(nil), auth.CABundle...)
	if proxyCAPEM, ok := os.LookupEnv(fleetgit.ProxyCABundleEnvVar); ok && proxyCAPEM != "" {
		proxyBytes := []byte(proxyCAPEM)
		tmpPool := x509.NewCertPool()
		if !tmpPool.AppendCertsFromPEM(proxyBytes) {
			logrus.Warnf("%s is set but contains no valid PEM certificates; ignoring proxy CA bundle", fleetgit.ProxyCABundleEnvVar)
		} else {
			if len(caBundle) > 0 && caBundle[len(caBundle)-1] != '\n' {
				caBundle = append(caBundle, '\n')
			}
			caBundle = append(caBundle, proxyBytes...)
		}
	}

	cloneOpts := &gogit.CloneOptions{
		URL:             cloneURL.String(),
		InsecureSkipTLS: auth.InsecureSkipVerify,
		CABundle:        caBundle,
		ProxyOptions:    fleetgit.ProxyOptsFromEnvironment(cloneURL.String()),
	}
	if err := setGitAuth(cloneOpts, &cloneURL, sshKeyPEM, auth); err != nil {
		return err
	}

	if err := resetDir(dst); err != nil {
		return err
	}

	if ref == "" {
		cloneOpts.Depth = d
		if _, err := gogit.PlainCloneContext(ctx, dst, false, cloneOpts); err != nil {
			return fmt.Errorf("shallow clone of %s: %w", cloneURL.Redacted(), err)
		}
		return nil
	}

	// Try shallow branch clone.
	branchOpts := *cloneOpts
	branchOpts.ReferenceName = plumbing.NewBranchReferenceName(ref)
	branchOpts.SingleBranch = true
	branchOpts.Depth = d
	if _, err := gogit.PlainCloneContext(ctx, dst, false, &branchOpts); err == nil {
		return nil
	}

	// Try shallow tag clone.
	if err := resetDir(dst); err != nil {
		return err
	}
	tagOpts := *cloneOpts
	tagOpts.ReferenceName = plumbing.NewTagReferenceName(ref)
	tagOpts.SingleBranch = true
	tagOpts.Depth = d
	if _, err := gogit.PlainCloneContext(ctx, dst, false, &tagOpts); err == nil {
		return nil
	}

	// Fall back: full clone then check out ref as commit SHA or arbitrary revision.
	if err := resetDir(dst); err != nil {
		return err
	}
	r, err := gogit.PlainCloneContext(ctx, dst, false, cloneOpts)
	if err != nil {
		return fmt.Errorf("cloning %s: %w", cloneURL.Redacted(), err)
	}
	h, err := r.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return fmt.Errorf("resolving ref %q: %w", ref, err)
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	return w.Checkout(&gogit.CheckoutOptions{Hash: *h})
}

// setGitAuth configures the authentication method on opts.
// sshKeyPEM (from the URL ?sshkey= param) takes precedence over auth.SSHPrivateKey.
// For HTTPS, credentials are taken from the URL userinfo first, then from auth.
func setGitAuth(opts *gogit.CloneOptions, u *url.URL, sshKeyPEM []byte, auth Auth) error {
	isSSH := u.Scheme == "ssh" || u.Scheme == "git"

	// Prefer the key from the URL query param over auth.SSHPrivateKey.
	keyPEM := sshKeyPEM
	if len(keyPEM) == 0 {
		keyPEM = auth.SSHPrivateKey
	}

	if len(keyPEM) > 0 {
		if !isSSH {
			// ?sshkey= in an HTTP(S) URL is a misconfiguration.
			if len(sshKeyPEM) > 0 {
				return fmt.Errorf("?sshkey= is not supported for %s URLs", u.Scheme)
			}
			// Auth.SSHPrivateKey alongside an HTTP(S) URL: ignore and fall
			// through to HTTP basic auth below.
			keyPEM = nil
		}
	}

	if len(keyPEM) > 0 {
		user := "git"
		if u.User != nil && u.User.Username() != "" {
			user = u.User.Username()
		}
		pubKeys, err := fleetssh.NewSSHPublicKeys(user, keyPEM, auth.SSHKnownHosts)
		if err != nil {
			return fmt.Errorf("configuring SSH auth: %w", err)
		}
		opts.Auth = pubKeys
		return nil
	}

	if u.Scheme == "https" || u.Scheme == "http" {
		var username, password string
		if u.User != nil {
			username = u.User.Username()
			password, _ = u.User.Password()
		}
		if username == "" {
			username = auth.Username
			password = auth.Password
		}
		if username != "" {
			opts.Auth = &httpgit.BasicAuth{Username: username, Password: password}
		}
	}
	return nil
}

// extractQueryParams strips the known Fleet query params (?ref=, ?sshkey=,
// ?depth=) from u and returns them as typed values.
func extractQueryParams(u *url.URL) (ref string, sshKey []byte, depth int, err error) {
	q := u.Query()
	ref = q.Get("ref")
	q.Del("ref")

	if raw := q.Get("sshkey"); raw != "" {
		// Prefer raw URL-safe Base64 (no padding). Fall back to standard/padded
		// Base64, normalising spaces to '+' first because URL query parsing
		// converts '+' to space in standard-encoded values.
		sshKey, err = base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			normalized := strings.ReplaceAll(raw, " ", "+")
			sshKey, err = base64.StdEncoding.DecodeString(normalized)
			if err != nil {
				sshKey, err = base64.URLEncoding.DecodeString(normalized)
				if err != nil {
					return "", nil, 0, fmt.Errorf("decoding sshkey query param: %w", err)
				}
			}
		}
	}
	q.Del("sshkey")

	if rawDepth := q.Get("depth"); rawDepth != "" {
		n, e := strconv.Atoi(rawDepth)
		if e != nil {
			return "", nil, 0, fmt.Errorf("invalid ?depth=%q: %w", rawDepth, e)
		}
		if n <= 0 {
			return "", nil, 0, fmt.Errorf("invalid ?depth=%d: must be > 0", n)
		}
		depth = n
	}
	q.Del("depth")

	// Strip only the Fleet-specific params from the raw query string,
	// preserving the encoding and ordering of all other parameters.
	// url.Values.Encode() would reorder keys and normalise escaping, which
	// breaks presigned or otherwise byte-stable URLs that contain non-Fleet
	// params (e.g. ?token=ab+cd%2Fef must survive unchanged).
	if u.RawQuery != "" {
		var kept []string
		for pair := range strings.SplitSeq(u.RawQuery, "&") {
			if pair == "" {
				continue
			}
			rawKey, _, _ := strings.Cut(pair, "=")
			key, _ := url.QueryUnescape(rawKey)
			switch key {
			case "ref", "sshkey", "depth":
				// Fleet-only params; already extracted above.
			default:
				kept = append(kept, pair)
			}
		}
		u.RawQuery = strings.Join(kept, "&")
	}
	return ref, sshKey, depth, nil
}

// resetDir removes path if it exists and recreates it as an empty directory.
func resetDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0750)
}

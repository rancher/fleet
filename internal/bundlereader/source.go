package bundlereader

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// sourceInfo describes a fully-resolved source for GetContent.
type sourceInfo struct {
	// scheme is the resolved scheme: "git", "http", or "local".
	scheme string
	// rawURL is the URL or local path to fetch (forced-scheme prefix and "//" removed).
	rawURL string
	// subDir is the subdirectory to copy out of the downloaded content (from the "//" notation).
	subDir string
}

// parseSource resolves a source string into a sourceInfo.
//
// It recognises:
//   - forced-scheme prefixes: "git::https://...", "ssh::...", "http::"
//   - the "//" subdirectory separator: "git::https://host/repo.git//path/to/dir"
//   - shorthand URLs: "github.com/user/repo", "gitlab.com/user/repo"
//   - SCP-style SSH: "git@host:org/repo"
//
// Anything that cannot be resolved to an HTTP or git URL is treated as a local path.
func parseSource(src, pwd string) (sourceInfo, error) {
	if src == "" {
		return sourceInfo{scheme: "local", rawURL: pwd}, nil
	}

	// 1. Strip forced-scheme prefix (e.g. "git::", "ssh::", "http::").
	forcedScheme, stripped := splitForcedScheme(src)

	// 2. Extract the "//" subdirectory separator from the stripped URL.
	cleanSrc, subDir := splitSubdir(stripped)

	// 3. A forced getter scheme determines the handler directly.
	switch forcedScheme {
	case "git", "ssh":
		// Normalise SCP-style SSH (e.g. "git@host:org/repo" or "user@host:org/repo")
		// so that downstream url.Parse works correctly.
		return sourceInfo{scheme: "git", rawURL: normalizeSCPToSSH(cleanSrc), subDir: subDir}, nil
	case "http", "https":
		return sourceInfo{scheme: "http", rawURL: cleanSrc, subDir: subDir}, nil
	}

	// 4. Parse the URL scheme.
	if u, err := url.Parse(cleanSrc); err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "git", "ssh":
			return sourceInfo{scheme: "git", rawURL: cleanSrc, subDir: subDir}, nil
		case "http", "https":
			return sourceInfo{scheme: "http", rawURL: cleanSrc, subDir: subDir}, nil
		}
	}

	// 5. Try shorthand detectors (GitHub, GitLab, SCP-SSH).
	if detected, ok, err := detectShorthand(cleanSrc); err != nil {
		return sourceInfo{}, fmt.Errorf("detecting source URL: %w", err)
	} else if ok {
		// detected is "git::..." — recurse to normalise, then merge subdirs.
		info, err := parseSource(detected, pwd)
		if err != nil {
			return sourceInfo{}, err
		}
		// Outer subDir appended after any subDir extracted by the detector.
		if subDir != "" {
			if info.subDir != "" {
				info.subDir = info.subDir + "/" + subDir
			} else {
				info.subDir = subDir
			}
		}
		return info, nil
	}

	// 6. Fall back to local path. Resolve relative paths against pwd so that the
	// caller does not need to track the working directory separately.
	rawPath := cleanSrc
	if !filepath.IsAbs(rawPath) && pwd != "" {
		rawPath = filepath.Join(pwd, rawPath)
	}
	return sourceInfo{scheme: "local", rawURL: rawPath, subDir: subDir}, nil
}

// splitForcedScheme splits a "scheme::url" string into the forced scheme and the rest.
// Recognised schemes are "git", "ssh", "http", and "https". Returns an empty scheme
// and the original src unchanged when no known forced-scheme prefix is present.
func splitForcedScheme(src string) (string, string) {
	scheme, rest, ok := strings.Cut(src, "::")
	if !ok {
		return "", src
	}
	switch scheme {
	case "git", "ssh", "http", "https":
		return scheme, rest
	}
	return "", src
}

// splitSubdir extracts the "//" subdirectory separator from src, mirroring
// go-getter's SourceDirSubdir semantics.
//
//	"https://host/repo?ref=main"          → ("https://host/repo?ref=main", "")
//	"https://host/repo.git//subdir"       → ("https://host/repo.git", "subdir")
//	"https://host/repo.git//sub?ref=main" → ("https://host/repo.git?ref=main", "sub")
func splitSubdir(src string) (string, string) {
	// Do not search inside the query string.
	stop := len(src)
	if idx := strings.Index(src, "?"); idx > -1 {
		stop = idx
	}

	// Skip past "://" to avoid treating the scheme separator as a subdir marker.
	offset := 0
	if idx := strings.Index(src[:stop], "://"); idx > -1 {
		offset = idx + 3
	}

	idx := strings.Index(src[offset:stop], "//")
	if idx == -1 {
		return src, ""
	}

	idx += offset
	subDir := src[idx+2:]
	base := src[:idx]

	// Move any query string from the subdir portion back onto the base URL.
	if qi := strings.Index(subDir, "?"); qi > -1 {
		base += subDir[qi:]
		subDir = subDir[:qi]
	}
	return base, subDir
}

// detectShorthand runs the known shorthand detectors (GitHub, GitLab, BitBucket,
// SCP-SSH) against src. Returns the resolved URL string, true, nil on a match.
func detectShorthand(src string) (string, bool, error) {
	if s, ok, err := detectGitHub(src); err != nil || ok {
		return s, ok, err
	}
	if s, ok, err := detectGitLab(src); err != nil || ok {
		return s, ok, err
	}
	if s, ok, err := detectBitBucket(src); err != nil || ok {
		return s, ok, err
	}
	if s, ok := detectSCPSSH(src); ok {
		return s, true, nil
	}
	return "", false, nil
}

// detectGitHub turns "github.com/user/repo[/subpath]" into a git:: HTTPS URL.
func detectGitHub(src string) (string, bool, error) {
	if !strings.HasPrefix(src, "github.com/") {
		return "", false, nil
	}
	return buildGitHTTPS(src, "github.com")
}

// detectGitLab turns "gitlab.com/user/repo[/subpath]" into a git:: HTTPS URL.
func detectGitLab(src string) (string, bool, error) {
	if !strings.HasPrefix(src, "gitlab.com/") {
		return "", false, nil
	}
	return buildGitHTTPS(src, "gitlab.com")
}

// detectBitBucket turns "bitbucket.org/user/repo[/subpath]" into a git:: HTTPS URL.
// Bitbucket dropped Mercurial support in 2020; all repositories are git today,
// so the VCS type can be inferred without a live API call.
func detectBitBucket(src string) (string, bool, error) {
	if !strings.HasPrefix(src, "bitbucket.org/") {
		return "", false, nil
	}
	return buildGitHTTPS(src, "bitbucket.org")
}

// buildGitHTTPS constructs "git::https://host/user/repo[.git][//subpath]" from
// "host/user/repo[/subpath]". The fourth path component and beyond become the
// subdirectory within the repo.
func buildGitHTTPS(src, host string) (string, bool, error) {
	// parts: [host, user, repo, rest...]
	parts := strings.SplitN(src, "/", 4)
	if len(parts) < 3 {
		return "", false, fmt.Errorf("%s URLs must have the form %s/user/repo", host, host)
	}

	repoURL, err := url.Parse(fmt.Sprintf("https://%s/%s/%s", parts[0], parts[1], parts[2]))
	if err != nil {
		return "", true, fmt.Errorf("parsing %s URL: %w", host, err)
	}
	if !strings.HasSuffix(repoURL.Path, ".git") {
		repoURL.Path += ".git"
	}

	result := "git::" + repoURL.String()
	if len(parts) == 4 && parts[3] != "" {
		result += "//" + parts[3]
	}
	return result, true, nil
}

// normalizeSCPToSSH converts SCP-style addresses like "user@host:path" to
// "ssh://user@host/path". Returns src unchanged if it is not SCP-style.
func normalizeSCPToSSH(src string) string {
	if strings.Contains(src, "://") {
		return src
	}
	userHost, path, ok := strings.Cut(src, ":")
	if !ok || path == "" || !strings.Contains(userHost, "@") {
		return src
	}
	return "ssh://" + userHost + "/" + strings.TrimPrefix(path, "/")
}

// detectSCPSSH detects SCP-style SSH addresses ("user@host:org/repo") and converts
// them to "git::ssh://user@host/org/repo". Requires an explicit user ("@" present)
// to avoid false positives against local "drive:path" or "host:port" strings.
func detectSCPSSH(src string) (string, bool) {
	if strings.Contains(src, "://") {
		return "", false
	}
	userHost, path, ok := strings.Cut(src, ":")
	if !ok || path == "" {
		return "", false
	}
	path = strings.TrimPrefix(path, "/")
	user, host, ok := strings.Cut(userHost, "@")
	if !ok || user == "" {
		return "", false
	}
	repoURL := &url.URL{
		Scheme: "ssh",
		User:   url.User(user),
		Host:   host,
		Path:   "/" + path,
	}
	return "git::" + repoURL.String(), true
}

// redactURL redacts credentials embedded in a raw Fleet source string.
//
// It strips any password from URL userinfo and removes the sshkey query
// parameter (which can carry a private key). It also handles Fleet-specific
// notations that url.Parse alone cannot deal with: forced-scheme prefixes
// ("git::https://…", "ssh::…") and the "//" subdirectory separator
// ("git::https://user:pass@host/repo//charts").
// For strings that carry neither credentials nor sensitive query params, or
// that cannot be parsed, the input is returned unchanged.
func redactURL(src string) string {
	forcedScheme, stripped := splitForcedScheme(src)
	cleanSrc, subDir := splitSubdir(stripped)

	u, err := url.Parse(cleanSrc)
	if err != nil {
		return src
	}

	// Remove sshkey query param; it can carry a private key.
	hasSensitiveQuery := u.Query().Has("sshkey")
	if u.User == nil && !hasSensitiveQuery {
		return src
	}

	if hasSensitiveQuery {
		q := u.Query()
		q.Del("sshkey")
		u.RawQuery = q.Encode()
	}

	redacted := u.Redacted()
	if subDir != "" {
		redacted += "//" + subDir
	}
	if forcedScheme != "" {
		return forcedScheme + "::" + redacted
	}
	return redacted
}

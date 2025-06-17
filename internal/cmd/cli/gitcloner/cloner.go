package gitcloner

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"

	fleetgit "github.com/rancher/fleet/pkg/git"
	giturls "github.com/rancher/fleet/pkg/git-urls"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"

	fleetssh "github.com/rancher/fleet/internal/ssh"
)

const defaultBranch = "master"

var (
	plainClone       = git.PlainClone
	readFile         = os.ReadFile
	fileStat         = os.Stat
	getGitHubAppAuth = fleetgit.GetGitHubAppAuth
)

type Cloner struct{}

// Can this be removed?
type Options struct {
	Repo            string
	Branch          string
	Auth            transport.AuthMethod
	InsecureSkipTLS bool
	CABundle        []byte
}

func New() *Cloner {
	return &Cloner{}
}

func (c *Cloner) CloneRepo(opts *GitCloner) error {
	url, err := giturls.Parse(opts.Repo)
	if err != nil {
		return fmt.Errorf("failed to parse git URL: %w", err)
	}
	if strings.HasPrefix(url.String(), "ssh://") {
		if opts.SSHPrivateKeyFile == "" {
			return fmt.Errorf("SSH private key file is required for SSH/SCP-style URLs: %s", url)
		}
	}

	// Azure DevOps requires capabilities multi_ack / multi_ack_detailed,
	// which are not fully implemented and by default are included in
	// transport.UnsupportedCapabilities.
	// Public repos in Azure can't be cloned.
	// This can be removed once go-git implements the git v2 protocol.
	// https://github.com/go-git/go-git/issues/64
	transport.UnsupportedCapabilities = []capability.Capability{
		capability.ThinPack,
	}
	auth, err := createAuthFromOpts(opts)
	if err != nil {
		return fmt.Errorf("failed to create auth from options for %s: %w", repo(opts), err)
	}
	caBundle, err := getCABundleFromFile(opts.CABundleFile)
	if err != nil {
		return fmt.Errorf("failed to read CA bundle from file for %s: %w", repo(opts), err)
	}

	if opts.Branch == "" && opts.Revision == "" {
		opts.Branch = defaultBranch
		return cloneBranch(opts, auth, caBundle)
	}

	if opts.Branch != "" {
		if opts.Revision != "" {
			logrus.Warn("Using branch for cloning the repo. Revision will be skipped.")
		}
		return cloneBranch(opts, auth, caBundle)
	}

	return cloneRevision(opts, auth, caBundle)
}

func cloneBranch(opts *GitCloner, auth transport.AuthMethod, caBundle []byte) error {
	_, err := plainClone(opts.Path, false, &git.CloneOptions{
		URL:               opts.Repo,
		Auth:              auth,
		InsecureSkipTLS:   opts.InsecureSkipTLS,
		CABundle:          caBundle,
		SingleBranch:      true,
		ReferenceName:     plumbing.ReferenceName(opts.Branch),
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
	})

	if err != nil {
		return fmt.Errorf("failed to clone repo from branch %s: %w", repo(opts), err)
	}
	return nil
}

func cloneRevision(opts *GitCloner, auth transport.AuthMethod, caBundle []byte) error {
	r, err := plainClone(opts.Path, false, &git.CloneOptions{
		URL:               opts.Repo,
		Auth:              auth,
		InsecureSkipTLS:   opts.InsecureSkipTLS,
		CABundle:          caBundle,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
	})
	if err != nil {
		return fmt.Errorf("failed to clone repo from revision %s: %w", repo(opts), err)
	}
	h, err := r.ResolveRevision(plumbing.Revision(opts.Revision))
	if err != nil {
		return fmt.Errorf("failed to resolve revision %s: %w", repo(opts), err)
	}
	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get filesystem worktree for %s: %w", repo(opts), err)
	}

	if err := w.Checkout(&git.CheckoutOptions{Hash: *h}); err != nil {
		return fmt.Errorf("failed to checkout in worktree %s: %w", repo(opts), err)
	}

	return nil
}

func getCABundleFromFile(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	return readFile(path)
}

// createAuthFromOpts adds auth for cloning git repos based on the parameters provided in opts.
func createAuthFromOpts(opts *GitCloner) (transport.AuthMethod, error) {
	knownHosts, isKnownHostsSet := os.LookupEnv(fleetssh.KnownHostsEnvVar)
	if knownHosts == "" {
		isKnownHostsSet = false
	}

	if opts.SSHPrivateKeyFile != "" {
		privateKey, err := readFile(opts.SSHPrivateKeyFile)
		if err != nil {
			return nil, err
		}
		gitURL, err := giturls.Parse(opts.Repo)
		if err != nil {
			return nil, err
		}
		auth, err := gossh.NewPublicKeys(gitURL.User.Username(), privateKey, "")
		if err != nil {
			return nil, err
		}
		if isKnownHostsSet {
			knownHostsCallBack, err := fleetssh.CreateKnownHostsCallBack([]byte(knownHosts))
			if err != nil {
				return nil, fmt.Errorf("could not create known_hosts callback: %w", err)
			}

			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		}
		return auth, nil
	}

	if opts.GitHubAppID != 0 && opts.GitHubAppInstallation != 0 && opts.GitHubAppKeyFile != "" {
		if _, err := fileStat(opts.GitHubAppKeyFile); err != nil {
			return nil, fmt.Errorf("failed to read GitHub app private key from file: %w", err)
		}

		key, err := readFile(opts.GitHubAppKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read GitHub app private key from file: %w", err)
		}

		auth, err := getGitHubAppAuth(int64(opts.GitHubAppID), int64(opts.GitHubAppInstallation), key)
		if err != nil {
			return nil, err
		}
		return auth, nil
	}

	if opts.Username != "" && opts.PasswordFile != "" {
		password, err := readFile(opts.PasswordFile)
		if err != nil {
			return nil, err
		}

		return &httpgit.BasicAuth{
			Username: opts.Username,
			Password: string(password),
		}, nil
	}

	return nil, nil
}

func repo(opts *GitCloner) string {
	return fmt.Sprintf("repo=%q branch=%q revision=%q path=%q", opts.Repo, opts.Branch, opts.Revision, opts.Path)
}

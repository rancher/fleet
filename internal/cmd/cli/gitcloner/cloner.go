package gitcloner

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"

	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule"
	fleetgithub "github.com/rancher/fleet/internal/github"
	fleetssh "github.com/rancher/fleet/internal/ssh"
	giturls "github.com/rancher/fleet/pkg/git-urls"
)

const defaultBranch = "master"

var (
	plainClone                                 = git.PlainClone
	cloneCommit                                = cloneCommitShallow
	updateSubmodules                           = submodule.UpdateSubmodules
	readFile                                   = os.ReadFile
	fileStat                                   = os.Stat
	appAuthGetter    fleetgithub.AppAuthGetter = fleetgithub.DefaultAppAuthGetter{}
)

type Cloner struct{}

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
	r, err := shallowCloneRef(opts, auth, caBundle, plumbing.ReferenceName(opts.Branch))
	if err != nil {
		return fmt.Errorf("failed to clone main repo from branch %s: %w, skipping submodule clone", repo(opts), err)
	}

	return updateSubmodulesShallow(r, auth)
}

func cloneRevision(opts *GitCloner, auth transport.AuthMethod, caBundle []byte) error {
	// A bare 40-hex revision resolves to the commit object (git ignores any
	// ref with that name), so skip the tag/branch attempts and fetch the exact
	// commit shallowly. The full-clone fallback covers the rare case where the
	// 40-hex value is actually a ref pointing at some other commit.
	if plumbing.IsHash(opts.Revision) {
		if err := cloneCommit(opts, auth, caBundle); err == nil {
			return nil
		}
		if err := resetDir(opts.Path); err != nil {
			return fmt.Errorf("failed to reset clone dir for %s: %w", repo(opts), err)
		}
		return fullCloneRevision(opts, auth, caBundle)
	}

	if r, err := shallowCloneRef(opts, auth, caBundle, plumbing.NewTagReferenceName(opts.Revision)); err == nil {
		return updateSubmodulesShallow(r, auth)
	}
	if err := resetDir(opts.Path); err != nil {
		return fmt.Errorf("failed to reset clone dir for %s: %w", repo(opts), err)
	}

	if r, err := shallowCloneRef(opts, auth, caBundle, plumbing.NewBranchReferenceName(opts.Revision)); err == nil {
		return updateSubmodulesShallow(r, auth)
	}
	if err := resetDir(opts.Path); err != nil {
		return fmt.Errorf("failed to reset clone dir for %s: %w", repo(opts), err)
	}

	return fullCloneRevision(opts, auth, caBundle)
}

// shallowCloneRef performs a depth-1, single-branch clone pinned to ref. It
// only owns the clone and leaves submodule handling to the caller, so the
// revision path can fall through to the next strategy on error while the
// branch path can wrap the error and update submodules on success.
func shallowCloneRef(opts *GitCloner, auth transport.AuthMethod, caBundle []byte, ref plumbing.ReferenceName) (*git.Repository, error) {
	return plainClone(opts.Path, false, &git.CloneOptions{
		URL:               opts.Repo,
		Depth:             1,
		Auth:              auth,
		InsecureSkipTLS:   opts.InsecureSkipTLS,
		CABundle:          caBundle,
		SingleBranch:      true,
		ReferenceName:     ref,
		RecurseSubmodules: git.NoRecurseSubmodules,
		Tags:              git.NoTags,
	})
}

// cloneCommitShallow fetches a single commit by SHA with depth 1, avoiding the
// full-history clone that resolving an arbitrary commit would otherwise need.
// It only succeeds when the server allows fetching an exact SHA
// (uploadpack.allowReachableSHA1InWant / allowAnySHA1InWant); callers must fall
// back to a full clone on error.
func cloneCommitShallow(opts *GitCloner, auth transport.AuthMethod, caBundle []byte) error {
	r, err := git.PlainInit(opts.Path, false)
	if err != nil {
		return err
	}
	if _, err := r.CreateRemote(&config.RemoteConfig{
		Name: git.DefaultRemoteName,
		URLs: []string{opts.Repo},
	}); err != nil {
		return err
	}
	// Store the fetched commit under a normal local ref, matching the SHA-fetch
	// strategies in submodule/strategy. The checkout below reads the object by
	// hash, so the destination ref name itself is irrelevant.
	refSpec := config.RefSpec(opts.Revision + ":refs/heads/temp")
	if err := r.Fetch(&git.FetchOptions{
		RemoteName:      git.DefaultRemoteName,
		Depth:           1,
		RefSpecs:        []config.RefSpec{refSpec},
		Auth:            auth,
		InsecureSkipTLS: opts.InsecureSkipTLS,
		CABundle:        caBundle,
		Tags:            git.NoTags,
	}); err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	if err := w.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(opts.Revision)}); err != nil {
		return err
	}
	return updateSubmodulesShallow(r, auth)
}

// fullCloneRevision clones the whole repository (all history and tags) and
// resolves opts.Revision locally. This is the slowest path and is only used
// when no cheaper strategy can satisfy the revision.
func fullCloneRevision(opts *GitCloner, auth transport.AuthMethod, caBundle []byte) error {
	r, err := plainClone(opts.Path, false, &git.CloneOptions{
		URL:               opts.Repo,
		Auth:              auth,
		InsecureSkipTLS:   opts.InsecureSkipTLS,
		CABundle:          caBundle,
		RecurseSubmodules: git.NoRecurseSubmodules,
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
	return updateSubmodulesShallow(r, auth)
}

// updateSubmodulesShallow recursively initializes and shallowly updates the
// repository's submodules. It is shared by every clone path.
func updateSubmodulesShallow(r *git.Repository, auth transport.AuthMethod) error {
	return updateSubmodules(r, &git.SubmoduleUpdateOptions{
		Init:              true,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		Depth:             1,
		Auth:              auth,
	})
}

// resetDir clears the clone destination so a subsequent clone attempt starts
// from a clean slate. go-git refuses to clone into a non-empty directory.
func resetDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0750)
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
			//nolint:gosec // G106: Use of ssh InsecureIgnoreHostKey should be audited - this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		}
		return auth, nil
	}

	if opts.GitHubAppID != 0 && opts.GitHubAppInstallation != 0 && opts.GitHubAppKeyFile != "" {
		if _, err := fileStat(opts.GitHubAppKeyFile); err != nil {
			return nil, fmt.Errorf("failed to resolve GitHub app private key from path: %w", err)
		}

		key, err := readFile(opts.GitHubAppKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read GitHub app private key from file: %w", err)
		}

		auth, err := appAuthGetter.Get(opts.Repo, opts.GitHubAppID, opts.GitHubAppInstallation, key)
		if err != nil {
			return nil, err
		}
		return auth, nil
	}

	if opts.PasswordFile != "" {
		password, err := readFile(opts.PasswordFile)
		if err != nil {
			return nil, err
		}

		if len(opts.Username) == 0 {
			return &httpgit.BasicAuth{
				Username: string(password),
			}, nil
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

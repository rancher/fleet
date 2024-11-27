package gitcloner

import (
	"fmt"
	"os"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"

	giturls "github.com/rancher/fleet/pkg/git-urls"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const defaultBranch = "master"

var (
	plainClone = git.PlainClone
	readFile   = os.ReadFile
)

type Cloner struct{}

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
		return err
	}
	caBundle, err := getCABundleFromFile(opts.CABundleFile)
	if err != nil {
		return err
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

	return err
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
		return err
	}
	h, err := r.ResolveRevision(plumbing.Revision(opts.Revision))
	if err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}

	return w.Checkout(&git.CheckoutOptions{
		Hash: *h,
	})
}

func getCABundleFromFile(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	return readFile(path)
}

// createAuthFromOpts adds auth for cloning git repos based on the parameters provided in opts.
func createAuthFromOpts(opts *GitCloner) (transport.AuthMethod, error) {
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return nil, err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			//this will run in an init-container, so there is no persistence
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
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
		if opts.KnownHostsFile != "" {
			knownHosts, err := readFile(opts.KnownHostsFile)
			if err != nil {
				return nil, err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err !=

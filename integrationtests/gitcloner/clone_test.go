package apply

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rancher/fleet/internal/cmd/cli/gitcloner"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/gogits/go-gogs-client"
	cp "github.com/otiai10/copy"
	"github.com/testcontainers/testcontainers-go"
	"golang.org/x/crypto/ssh/knownhosts"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
)

func init() {
	utils.DisableReaper()
}

/*
These tests use gogs for testing integration with a git server. Gogs container is created with testcontainers,
and uses data and conf from assets/gitserver. These files are mounted into the gogs container.
It contains an already created user whose credentials are provided as constants.
*/
const (
	testRepoName = "test-repo"
)

var (
	gogsClient   *gogs.Client
	gogsCABundle []byte
)

// This test starts gogs in a container outside the cluster. The exposed ports
// need to be reachable. Out of the box this does not work when the container
// runtime is in a VM, e.g. on Mac.
var _ = Describe("Applying a git job gets content from git repo", Label("networking"), Ordered, func() {

	var (
		opts          *gitcloner.GitCloner
		private       bool
		cloneErr      error
		tmp           string
		initialCommit string
		repoName      string
		container     testcontainers.Container
	)

	BeforeAll(func() {
		var err error
		container, gogsCABundle, gogsClient, err = utils.CreateGogsContainerWithHTTPS(context.Background(), "assets/gitserver")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			if container != nil {
				_ = container.Terminate(context.Background())
			}
		})
	})

	When("Cloning an https repo", func() {
		JustBeforeEach(func() {
			url, err := utils.GetGogsHTTPSURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			repoName, initialCommit, err = createRepo(url, "assets/repo", private)
			Expect(err).NotTo(HaveOccurred())
			opts.Repo = url + "/test/" + repoName
			tmp = utils.CreateTempFolder("gogs")
			opts.Path = tmp
		})

		JustAfterEach(func() {
			err := os.RemoveAll(tmp)
			Expect(err).NotTo(HaveOccurred())
		})

		When("cloning a public repo that contains a README.md file providing a branch", func() {
			BeforeEach(func() {
				private = false
				opts = &gitcloner.GitCloner{
					InsecureSkipTLS: true,
					Branch:          "master",
				}
			})

			JustBeforeEach(func() {
				c := gitcloner.New()
				cloneErr = c.CloneRepo(opts)
				Expect(cloneErr).NotTo(HaveOccurred())
			})

			It("clones the README.md file", func() {
				_, err := os.Stat(tmp + "/README.md")
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("cloning a public repo that contains a README.md file providing a revision", func() {
			BeforeEach(func() {
				private = false
				opts = &gitcloner.GitCloner{
					InsecureSkipTLS: true,
				}
			})

			JustBeforeEach(func() {
				c := gitcloner.New()
				opts.Revision = initialCommit
				cloneErr = c.CloneRepo(opts)
				Expect(cloneErr).NotTo(HaveOccurred())
			})

			It("clones the README.md file", func() {
				_, err := os.Stat(tmp + "/README.md")
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("the cloned repo is private and contains a README.md file", func() {
			When("No authentication is provided", func() {
				BeforeEach(func() {
					private = true
					opts = &gitcloner.GitCloner{
						InsecureSkipTLS: true,
					}
				})

				JustBeforeEach(func() {
					c := gitcloner.New()
					cloneErr = c.CloneRepo(opts)
				})

				It("Fails to clone the repo", func() {
					Expect(cloneErr.Error()).To(ContainSubstring("authentication required"))
				})
			})

			When("Basic authentication is provided", func() {
				BeforeEach(func() {
					private = true
					opts = &gitcloner.GitCloner{
						InsecureSkipTLS: true,
						Username:        utils.GogsUser,
						PasswordFile:    "assets/gogs/password",
					}
				})

				JustBeforeEach(func() {
					c := gitcloner.New()
					cloneErr = c.CloneRepo(opts)
					Expect(cloneErr).NotTo(HaveOccurred())
				})

				It("clones the README.md file", func() {
					_, err := os.Stat(tmp + "/README.md")
					Expect(err).NotTo(HaveOccurred())
				})
			})
		})

		When("Insecure skip tls is not true, and no caBundle is provided", func() {
			BeforeEach(func() {
				private = false
				opts = &gitcloner.GitCloner{}
			})

			JustBeforeEach(func() {
				c := gitcloner.New()
				cloneErr = c.CloneRepo(opts)
			})

			It("Fails to clone the repo", func() {
				Expect(cloneErr.Error()).To(ContainSubstring("x509: certificate signed by unknown authority"))
			})
		})

		When("Insecure skip tls is not true, and caBundle is provided", func() {
			var tmpFile string

			BeforeEach(func() {
				private = false
				opts = &gitcloner.GitCloner{}
			})

			AfterEach(func() {
				err := os.Remove(tmpFile)
				Expect(err).NotTo(HaveOccurred())
			})

			JustBeforeEach(func() {
				caBundleFile, err := os.CreateTemp("", "gitcloner")
				Expect(err).NotTo(HaveOccurred())
				tmpFile = caBundleFile.Name()
				_, err = caBundleFile.Write(gogsCABundle)
				Expect(err).NotTo(HaveOccurred())
				opts.CABundleFile = caBundleFile.Name()

				c := gitcloner.New()
				cloneErr = c.CloneRepo(opts)
			})

			It("clones the README.md file", func() {
				Expect(cloneErr).NotTo(HaveOccurred())
				_, err := os.Stat(tmp + "/README.md")
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	When("Cloning an ssh repo", func() {
		var tmpKey string

		JustBeforeEach(func() {
			url, err := utils.GetGogsHTTPSURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			repoName, _, err = createRepo(url, "assets/repo", private)
			Expect(err).NotTo(HaveOccurred())
			sshURL, err := utils.GetGogsSSHURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			opts.Repo = sshURL + "/test/" + repoName
			tmp = utils.CreateTempFolder("gogs")
			opts.Path = tmp
			// create private key, and register public key in gogs
			key, err := createAndAddKeys()
			Expect(err).NotTo(HaveOccurred())
			tmpFile, err := os.CreateTemp("", "testkey")
			Expect(err).NotTo(HaveOccurred())
			_, err = tmpFile.WriteString(key)
			Expect(err).NotTo(HaveOccurred())
			tmpKey = tmpFile.Name()
			opts.SSHPrivateKeyFile = tmpKey
		})

		JustAfterEach(func() {
			keys, err := gogsClient.ListMyPublicKeys()
			Expect(err).NotTo(HaveOccurred())
			for _, key := range keys {
				err := gogsClient.DeletePublicKey(key.ID)
				Expect(err).NotTo(HaveOccurred())
			}

			err = os.RemoveAll(tmp)
			Expect(err).NotTo(HaveOccurred())
		})

		When("the cloned repo is private and contains a README.md file", func() {
			BeforeEach(func() {
				private = true
				opts = &gitcloner.GitCloner{
					InsecureSkipTLS: true,
				}
			})

			AfterEach(func() {
				err := os.RemoveAll(tmpKey)
				Expect(err).NotTo(HaveOccurred())
			})

			JustBeforeEach(func() {
				c := gitcloner.New()
				Eventually(func() error {
					return c.CloneRepo(opts)
				}).ShouldNot(HaveOccurred())
			})

			It("clones the README.md file", func() {
				_, err := os.Stat(tmp + "/README.md")
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("a known_hosts file is provided", func() {
			var tmpKnownHosts string

			BeforeEach(func() {
				private = true
				opts = &gitcloner.GitCloner{
					InsecureSkipTLS: true,
				}
			})

			AfterEach(func() {
				err := os.RemoveAll(tmpKey)
				Expect(err).NotTo(HaveOccurred())
				err = os.RemoveAll(tmpKnownHosts)
				Expect(err).NotTo(HaveOccurred())
			})

			JustBeforeEach(func() {
				knownHostEntry, err := getKnownHostEntry(container)
				Expect(err).NotTo(HaveOccurred())

				err = os.Setenv("FLEET_KNOWN_HOSTS", knownHostEntry)
				Expect(err).NotTo(HaveOccurred())
			})

			It("clones successfully when a matching host key is provided", func() {
				c := gitcloner.New()
				Eventually(func() error {
					return c.CloneRepo(opts)
				}).ShouldNot(HaveOccurred())

				_, err := os.Stat(tmp + "/README.md")
				Expect(err).NotTo(HaveOccurred())
			})

			It("fails without a matching known host key", func() {
				tmpKnownHostsFile, err := os.CreateTemp("", "known_hosts")
				Expect(err).NotTo(HaveOccurred())

				tmpKnownHosts = tmpKnownHostsFile.Name()

				err = os.Setenv("FLEET_KNOWN_HOSTS", "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl")
				Expect(err).NotTo(HaveOccurred())

				c := gitcloner.New()
				Eventually(func() error {
					return c.CloneRepo(opts)
				}).Should(MatchError(&knownhosts.KeyError{}))
			})
		})
	})
})

// createRepo creates a git repo for testing
// returns the initial commit and a unique repo name.
func createRepo(url string, path string, private bool) (string, string, error) {
	repoName := fmt.Sprintf("%s%d", testRepoName, time.Now().UTC().UnixMicro())

	_, err := gogsClient.CreateRepo(gogs.CreateRepoOption{
		Name:    repoName,
		Private: private,
	})
	if err != nil {
		return "", "", err
	}
	repoURL := url + "/" + utils.GogsUser + "/" + repoName

	// add initial commit
	tmp, err := os.MkdirTemp("", repoName)
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	_, err = git.PlainInit(tmp, false)
	if err != nil {
		return "", "", err
	}
	r, err := git.PlainOpen(tmp)
	if err != nil {
		return "", "", err
	}
	w, err := r.Worktree()
	if err != nil {
		return "", "", err
	}
	err = cp.Copy(path, tmp)
	if err != nil {
		return "", "", err
	}
	_, err = w.Add(".")
	if err != nil {
		return "", "", err
	}
	commit, err := w.Commit("test commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test user",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", "", err
	}
	cfg, err := r.Config()
	if err != nil {
		return "", "", err
	}
	cfg.Remotes["upstream"] = &config.RemoteConfig{
		Name: "upstream",
		URLs: []string{repoURL},
	}
	err = r.SetConfig(cfg)
	if err != nil {
		return "", "", err
	}
	err = r.Push(&git.PushOptions{
		RemoteName: "upstream",
		RemoteURL:  repoURL,
		Auth: &httpgit.BasicAuth{
			Username: utils.GogsUser,
			Password: utils.GogsPass,
		},
		InsecureSkipTLS: true,
	})
	if err != nil {
		return "", "", err
	}

	return repoName, commit.String(), nil
}

// createAndAddKeys creates a public/private key pair, registers the public key in gogs,
// and returns the private key in PEM format.
func createAndAddKeys() (string, error) {
	publicKey, privateKey, err := utils.MakeSSHKeyPair()
	if err != nil {
		return "", err
	}

	_, err = gogsClient.CreatePublicKey(gogs.CreateKeyOption{
		Title: "test",
		Key:   publicKey,
	})
	if err != nil {
		return "", err
	}

	return privateKey, nil
}

func getKnownHostEntry(container testcontainers.Container) (string, error) {
	mappedPort, err := container.MappedPort(context.Background(), utils.GogsSSHPort)
	if err != nil {
		return "", err
	}
	port := mappedPort.Port()

	publicHostKey, err := container.CopyFileFromContainer(context.Background(), "/data/ssh/ssh_host_ecdsa_key.pub")
	if err != nil {
		return "", err
	}
	publicHostKeyBytes, err := io.ReadAll(publicHostKey)
	if err != nil {
		return "", err
	}
	fields := strings.Split(string(publicHostKeyBytes), " ")
	algorithm := fields[0]
	hostKey := fields[1]

	return fmt.Sprintf("[localhost]:%s %s %s", port, algorithm, hostKey), nil
}

package apply

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rancher/fleet/internal/cmd/cli/gitcloner"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockermount "github.com/docker/docker/api/types/mount"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/gogits/go-gogs-client"
	cp "github.com/otiai10/copy"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

/*
These tests use gogs for testing integration with a git server. Gogs container is created with testcontainers,
and uses data and conf from assets/gitserver. These files are mounted into the gogs container.
It contains an already created user whose credentials are provided as constants.
*/
const (
	gogsUser      = "test"
	gogsPass      = "pass"
	gogsHTTPSPort = "3000"
	gogsSSHPort   = "22"
	testRepoName  = "test-repo"
	timeout       = 120 * time.Second
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
		container, err = createGogsContainerWithHTTPS()
		Expect(err).NotTo(HaveOccurred())
	})

	When("Cloning an https repo", func() {
		JustBeforeEach(func() {
			url, err := getHTTPSURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			repoName, initialCommit, err = createRepo(url, "assets/repo", private)
			Expect(err).NotTo(HaveOccurred())
			opts.Repo = url + "/test/" + repoName
			tmp = createTempFolder()
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
						Username:        gogsUser,
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
			url, err := getHTTPSURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			repoName, _, err = createRepo(url, "assets/repo", private)
			Expect(err).NotTo(HaveOccurred())
			sshURL, err := getSSHURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			opts.Repo = sshURL + "/test/" + repoName
			tmp = createTempFolder()
			opts.Path = tmp
			// create private key, and register public key in gogs
			key, err := createAndAddKeys()
			Expect(err).NotTo(HaveOccurred())
			tmpFile, err := os.CreateTemp("", "testkey")
			Expect(err).NotTo(HaveOccurred())
			_, err = tmpFile.Write([]byte(key))
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
				// create known_hosts file
				knownHostEntry, err := getKnownHostEntry(container)
				Expect(err).NotTo(HaveOccurred())
				tmpKnownHostsFile, err := os.CreateTemp("", "known_hosts")
				Expect(err).NotTo(HaveOccurred())
				_, err = tmpKnownHostsFile.Write([]byte(knownHostEntry))
				Expect(err).NotTo(HaveOccurred())
				tmpKnownHosts = tmpKnownHostsFile.Name()
				opts.KnownHostsFile = tmpKnownHosts
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
				_, err = tmpKnownHostsFile.Write([]byte("github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"))
				Expect(err).NotTo(HaveOccurred())
				tmpKnownHosts = tmpKnownHostsFile.Name()
				opts.KnownHostsFile = tmpKnownHosts

				c := gitcloner.New()
				Eventually(func() error {
					return c.CloneRepo(opts)
				}).Should(MatchError(&knownhosts.KeyError{}))
			})
		})
	})

	AfterAll(func() {
		Expect(container.Terminate(context.Background())).NotTo(HaveOccurred())
	})
})

// createGogsContainerWithHTTPS creates a container that runs gogs. It creates a certificate for tls, and stores the ca bundle in gogsCABundle
func createGogsContainerWithHTTPS() (testcontainers.Container, error) {
	tmpDir := createTempFolder()
	err := cp.Copy("assets/gitserver", tmpDir)
	if err != nil {
		return nil, err
	}
	req := testcontainers.ContainerRequest{
		Image:        "gogs/gogs:0.13",
		ExposedPorts: []string{gogsHTTPSPort + "/tcp", gogsSSHPort + "/tcp"},
		WaitingFor:   wait.ForListeningPort("22/tcp").WithStartupTimeout(timeout),
		HostConfigModifier: func(hostConfig *dockercontainer.HostConfig) {
			hostConfig.Mounts = []dockermount.Mount{
				{
					Type:   dockermount.TypeBind,
					Source: tmpDir,
					Target: "/data",
				},
			}

		},
	}
	container, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})

	if err != nil {
		return nil, err
	}

	// create ca bundle and certs needed for https
	_, _, err = container.Exec(context.Background(), []string{"./gogs", "cert", "-ca=true", "-duration=8760h0m0s", "-host=localhost"})
	if err != nil {
		return nil, err
	}
	_, _, err = container.Exec(context.Background(), []string{"chown", "git:git", "cert.pem", "key.pem"})
	if err != nil {
		return nil, err
	}
	caReader, err := container.CopyFileFromContainer(context.Background(), "/app/gogs/cert.pem")
	if err != nil {
		return nil, err
	}
	gogsCABundle, err = io.ReadAll(caReader)
	if err != nil {
		return nil, err
	}

	// restart gogs container to make sure https certs are picked
	err = container.Stop(context.Background(), &[]time.Duration{timeout}[0])
	if err != nil {
		return nil, err
	}
	err = container.Start(context.Background())
	if err != nil {
		return nil, err
	}

	url, err := getHTTPSURL(context.Background(), container)
	if err != nil {
		return nil, err
	}

	// create access token, we need to wait until the https server is available. We can't check this in testcontainers.WaitFor
	// because we need to create the certificates first.
	Eventually(func() error {
		c := gogs.NewClient(url, "")
		//nolint:gosec // need insecure TLS option for testing
		conf := &tls.Config{InsecureSkipVerify: true}

		// only continue if it's a TLS connection
		addr := strings.Replace(url, "https://", "", 1)
		_, err := tls.Dial("tcp", addr, conf)
		if err != nil {
			return err
		}

		tr := &http.Transport{TLSClientConfig: conf}
		httpClient := &http.Client{Transport: tr} // #nosec G402
		c.SetHTTPClient(httpClient)
		token, err := c.CreateAccessToken(gogsUser, gogsPass, gogs.CreateAccessTokenOption{
			Name: "test",
		})
		if err != nil {
			return err
		}
		gogsClient = gogs.NewClient(url, token.Sha1)
		gogsClient.SetHTTPClient(httpClient)

		return nil
	}, timeout, "10s").ShouldNot(HaveOccurred())

	return container, nil
}

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
	repoURL := url + "/" + gogsUser + "/" + repoName

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
			Username: gogsUser,
			Password: gogsPass,
		},
		InsecureSkipTLS: true,
	})
	if err != nil {
		return "", "", err
	}

	return repoName, commit.String(), nil
}

func getHTTPSURL(ctx context.Context, container testcontainers.Container) (string, error) {
	mappedPort, err := container.MappedPort(ctx, gogsHTTPSPort)
	if err != nil {
		return "", err
	}
	host, err := container.Host(ctx)
	if err != nil {
		return "", err
	}
	url := "https://" + host + ":" + mappedPort.Port()

	return url, nil
}

func getSSHURL(ctx context.Context, container testcontainers.Container) (string, error) {
	mappedPort, err := container.MappedPort(ctx, gogsSSHPort)
	if err != nil {
		return "", err
	}
	host, err := container.Host(ctx)
	if err != nil {
		return "", err
	}
	url := "ssh://git@" + host + ":" + mappedPort.Port()

	return url, nil
}

// createTempFolder uses testing tempDir if running in local, which will cleanup the files at the end of the tests.
// cleanup fails in github actions, that's why we use os.MkdirTemp instead. Container will be removed at the end in
// github actions, so no resources are left orphaned.
func createTempFolder() string {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		tmp, err := os.MkdirTemp("", "gogs")
		Expect(err).ToNot(HaveOccurred())
		return tmp
	}

	return GinkgoT().TempDir()
}

// createAndAddKeys creates a public private key pair. It adds the public key to gogs, and returns the private key.
func createAndAddKeys() (string, error) {
	publicKey, privateKey, err := makeSSHKeyPair()
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
	mappedPort, err := container.MappedPort(context.Background(), gogsSSHPort)
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

func makeSSHKeyPair() (string, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", err
	}

	var privKeyBuf strings.Builder
	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}
	if err := pem.Encode(&privKeyBuf, privateKeyPEM); err != nil {
		return "", "", err
	}
	pub, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}
	var pubKeyBuf strings.Builder
	pubKeyBuf.Write(ssh.MarshalAuthorizedKey(pub))

	return pubKeyBuf.String(), privKeyBuf.String(), nil
}

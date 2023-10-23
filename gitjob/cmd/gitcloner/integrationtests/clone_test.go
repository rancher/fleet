package apply

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/gogits/go-gogs-client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	cp "github.com/otiai10/copy"
	"github.com/rancher/gitjob/cmd/gitcloner/cmd"
	"github.com/rancher/gitjob/cmd/gitcloner/gogit"
	"github.com/testcontainers/testcontainers-go"
	"golang.org/x/crypto/ssh"
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
	timeout       = 60 * time.Second
)

var (
	gogsClient   *gogs.Client
	gogsCABundle []byte
)

var _ = Describe("Applying a git job gets content from git repo", func() {

	var (
		opts          *cmd.Options
		private       bool
		cloneErr      error
		tmp           string
		initialCommit string
	)

	When("Cloning an https repo", func() {
		JustBeforeEach(func() {
			container, err := createGogsContainerWithHTTPS()
			Expect(err).NotTo(HaveOccurred())
			url, err := getHTTPSURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			initialCommit, err = createRepo(url, "assets/repo", private)
			Expect(err).NotTo(HaveOccurred())
			opts.Repo = url + "/test/" + testRepoName
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
				opts = &cmd.Options{
					InsecureSkipTLS: true,
					Branch:          "master",
				}
			})

			JustBeforeEach(func() {
				c := gogit.NewCloner()
				cloneErr := c.CloneRepo(opts)
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
				opts = &cmd.Options{
					InsecureSkipTLS: true,
				}
			})

			JustBeforeEach(func() {
				c := gogit.NewCloner()
				opts.Revision = initialCommit
				cloneErr := c.CloneRepo(opts)
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
					opts = &cmd.Options{
						InsecureSkipTLS: true,
					}
				})

				JustBeforeEach(func() {
					c := gogit.NewCloner()
					cloneErr = c.CloneRepo(opts)
				})

				It("Fails to clone the repo", func() {
					Expect(cloneErr.Error()).To(Equal("authentication required"))
				})
			})

			When("Basic authentication is provided", func() {
				BeforeEach(func() {
					private = true
					opts = &cmd.Options{
						InsecureSkipTLS: true,
						Username:        gogsUser,
						PasswordFile:    "assets/gogs/password",
					}
				})

				JustBeforeEach(func() {
					c := gogit.NewCloner()
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
				opts = &cmd.Options{}
			})

			JustBeforeEach(func() {
				c := gogit.NewCloner()
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
				opts = &cmd.Options{}
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

				c := gogit.NewCloner()
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
			container, err := createGogsContainerWithHTTPS()
			Expect(err).NotTo(HaveOccurred())
			url, err := getHTTPSURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			_, err = createRepo(url, "assets/repo", private)
			Expect(err).NotTo(HaveOccurred())
			sshURL, err := getSSHURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			opts.Repo = sshURL + "/test/" + testRepoName
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
			err := os.RemoveAll(tmp)
			Expect(err).NotTo(HaveOccurred())
		})

		When("the cloned repo is private and contains a README.md file", func() {
			BeforeEach(func() {
				private = true
				opts = &cmd.Options{
					InsecureSkipTLS: true,
				}
			})

			AfterEach(func() {
				err := os.RemoveAll(tmpKey)
				Expect(err).NotTo(HaveOccurred())
			})

			JustBeforeEach(func() {
				c := gogit.NewCloner()
				cloneErr = c.CloneRepo(opts)
				Expect(cloneErr).NotTo(HaveOccurred())
			})

			It("clones the README.md file", func() {
				_, err := os.Stat(tmp + "/README.md")
				Expect(err).NotTo(HaveOccurred())
			})
		})
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
		Mounts: testcontainers.ContainerMounts{
			{
				Source: testcontainers.GenericBindMountSource{HostPath: tmpDir},
				Target: "/data",
			},
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
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
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
	}, timeout, "200ms").ShouldNot(HaveOccurred())

	return container, nil
}

// createRepo creates a git repo for testing
func createRepo(url string, path string, private bool) (string, error) {
	_, err := gogsClient.CreateRepo(gogs.CreateRepoOption{
		Name:    testRepoName,
		Private: private,
	})
	if err != nil {
		return "", err
	}
	repoURL := url + "/" + gogsUser + "/" + testRepoName

	// add initial commit
	tmp, err := os.MkdirTemp("", testRepoName)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	_, err = git.PlainInit(tmp, false)
	if err != nil {
		return "", err
	}
	r, err := git.PlainOpen(tmp)
	if err != nil {
		return "", err
	}
	w, err := r.Worktree()
	if err != nil {
		return "", err
	}
	err = cp.Copy(path, tmp)
	if err != nil {
		return "", err
	}
	_, err = w.Add(".")
	if err != nil {
		return "", err
	}
	commit, err := w.Commit("test commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test user",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", err
	}
	cfg, err := r.Config()
	if err != nil {
		return "", err
	}
	cfg.Remotes["upstream"] = &config.RemoteConfig{
		Name: "upstream",
		URLs: []string{repoURL},
	}
	err = r.SetConfig(cfg)
	if err != nil {
		return "", err
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
		return "", err
	}

	return commit.String(), nil
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

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

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/gogits/go-gogs-client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	cp "github.com/otiai10/copy"
	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/testcontainers/testcontainers-go"
	"golang.org/x/crypto/ssh"
)

const (
	gogsUser           = "test"
	gogsPass           = "pass"
	numResourcesSimple = 2
	numResourcesNested = 3
)

var (
	gogsClient   *gogs.Client
	gogsCABundle []byte
)

var _ = Describe("Fleet apply gets content from git repo", Ordered, func() {

	var (
		name    string
		options apply.Options
	)

	When("Public repo that contains simple resources", func() {
		BeforeEach(func() {
			name = "simple"
			_, url, err := createGogsContainer()
			Expect(err).NotTo(HaveOccurred())
			err = createRepo(url, cli.AssetsPath+name, false)
			Expect(err).NotTo(HaveOccurred())
			options = apply.Options{
				Output:             gbytes.NewBuffer(),
				GitRepo:            url + "/test/test-repo",
				GitInsecureSkipTLS: true,
			}
			err = fleetApply(name, []string{}, options)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Bundle is created with all the resources", func() {
			verifyBundleAndAllResourcesArePresent(options.Output, numResourcesSimple)
		})
	})

	When("Private repo that contains simple resources in a nested folder", func() {
		When("Basic authentication is provided", func() {
			BeforeEach(func() {
				name = "nested_simple"
				_, url, err := createGogsContainer()
				Expect(err).NotTo(HaveOccurred())
				err = createRepo(url, cli.AssetsPath+name, true)
				Expect(err).NotTo(HaveOccurred())
				options = apply.Options{
					Output:  gbytes.NewBuffer(),
					GitRepo: url + "/test/test-repo",
					GitAuth: &httpgit.BasicAuth{
						Username: gogsUser,
						Password: gogsPass,
					},
					GitInsecureSkipTLS: true,
				}
				err = fleetApply(name, []string{}, options)
				Expect(err).NotTo(HaveOccurred())
			})

			It("Bundle is created with all the resources", func() {
				verifyBundleAndAllResourcesArePresent(options.Output, numResourcesNested)
			})
		})

		When("SSH authentication is provided", func() {
			BeforeEach(func() {
				name = "nested_simple"
				container, url, err := createGogsContainer()
				Expect(err).NotTo(HaveOccurred())
				privateKey, err := createAndAddKeys()
				Expect(err).NotTo(HaveOccurred())
				err = createRepo(url, cli.AssetsPath+name, true)
				Expect(err).NotTo(HaveOccurred())
				auth, err := gossh.NewPublicKeys("git", []byte(privateKey), "")
				Expect(err).NotTo(HaveOccurred())
				auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
				sshPort, err := container.MappedPort(context.Background(), "22")
				Expect(err).NotTo(HaveOccurred())

				options = apply.Options{
					Output:             gbytes.NewBuffer(),
					GitRepo:            "ssh://git@localhost:" + sshPort.Port() + "/test/test-repo",
					GitAuth:            auth,
					GitInsecureSkipTLS: true,
				}
				Eventually(func() error {
					return fleetApply(name, []string{}, options)
				}).Should(Not(HaveOccurred()))
			})

			It("Bundle is created with all the resources", func() {
				verifyBundleAndAllResourcesArePresent(options.Output, numResourcesNested)
			})
		})

		When("Authentication is not provided", func() {
			It("Error is returned", func() {
				name = "nested_simple"
				_, url, err := createGogsContainer()
				Expect(err).NotTo(HaveOccurred())
				err = createRepo(url, cli.AssetsPath+name, true)
				Expect(err).NotTo(HaveOccurred())
				options = apply.Options{
					Output:             gbytes.NewBuffer(),
					GitRepo:            url + "/test/test-repo",
					GitInsecureSkipTLS: true,
				}
				err = fleetApply(name, []string{}, options)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("error cloning repo: authentication required"))
			})
		})

		When("GitInsecureSkipTLS is not set and caBundle is not provided", func() {
			It("TLS cert can't be verified error is returned", func() {
				name = "nested_simple"
				_, url, err := createGogsContainer()
				Expect(err).NotTo(HaveOccurred())
				err = createRepo(url, cli.AssetsPath+name, false)
				Expect(err).NotTo(HaveOccurred())
				options = apply.Options{
					Output:  gbytes.NewBuffer(),
					GitRepo: url + "/test/test-repo",
				}
				err = fleetApply(name, []string{}, options)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(`error cloning repo: Get "https://localhost:`))
				Expect(err.Error()).To(ContainSubstring(`tls: failed to verify certificate: x509: certificate signed by unknown authority`))
			})
		})

		When("GitInsecureSkipTLS is not set and caBundle is provided", func() {
			BeforeEach(func() {
				name = "simple"
				_, url, err := createGogsContainer()
				Expect(err).NotTo(HaveOccurred())
				err = createRepo(url, cli.AssetsPath+name, false)
				Expect(err).NotTo(HaveOccurred())
				options = apply.Options{
					Output:      gbytes.NewBuffer(),
					GitRepo:     url + "/test/test-repo",
					GitCABundle: gogsCABundle,
				}
				err = fleetApply(name, []string{}, options)
				Expect(err).NotTo(HaveOccurred())
			})

			It("Bundle is created with all the resources", func() {
				verifyBundleAndAllResourcesArePresent(options.Output, numResourcesSimple)
			})
		})
	})
})

// createGogsContainer creates a container that runs gogs. It creates a certificate for tls, and stores the ca bundle in gogsCABundle
func createGogsContainer() (testcontainers.Container, string, error) {
	tmpDir := createTempFolder()
	err := cp.Copy("../assets/gitserver", tmpDir)
	if err != nil {
		return nil, "", err
	}
	req := testcontainers.ContainerRequest{
		Image:        "gogs/gogs:0.13",
		ExposedPorts: []string{"3000/tcp", "22/tcp"},
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
		return nil, "", err
	}

	// create ca bundle
	_, _, err = container.Exec(context.Background(), []string{"./gogs", "cert", "-ca=true", "-duration=8760h0m0s", "-host=localhost"})
	if err != nil {
		return nil, "", err
	}
	_, _, err = container.Exec(context.Background(), []string{"chown", "git:git", "cert.pem", "key.pem"})
	if err != nil {
		return nil, "", err
	}
	caReader, err := container.CopyFileFromContainer(context.Background(), "/app/gogs/cert.pem")
	if err != nil {
		return nil, "", err
	}
	gogsCABundle, err = io.ReadAll(caReader)
	if err != nil {
		return nil, "", err
	}

	url, err := getURL(context.Background(), container)
	if err != nil {
		return nil, "", err
	}

	c := gogs.NewClient(url, "")
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient := &http.Client{Transport: tr}
	c.SetHTTPClient(httpClient)

	// create access token, we need to wait until the http server is available
	Eventually(func() error {
		token, err := c.CreateAccessToken(gogsUser, gogsPass, gogs.CreateAccessTokenOption{
			Name: "test",
		})
		if err != nil {
			return err
		}
		gogsClient = gogs.NewClient(url, token.Sha1)
		gogsClient.SetHTTPClient(httpClient)

		return nil
	}, "5s", "200ms").ShouldNot(HaveOccurred())

	return container, url, nil
}

// createRepo creates a git repo for testing
func createRepo(url string, path string, private bool) error {
	name := "test-repo"
	_, err := gogsClient.CreateRepo(gogs.CreateRepoOption{
		Name:    name,
		Private: private,
	})
	if err != nil {
		return err
	}
	repoURL := url + "/" + gogsUser + "/" + name

	// add initial commit
	tmp, err := os.MkdirTemp("", name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	_, err = gogit.PlainInit(tmp, false)
	if err != nil {
		return err
	}
	r, err := gogit.PlainOpen(tmp)
	if err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	err = cp.Copy(path, tmp)
	if err != nil {
		return err
	}
	_, err = w.Add(".")
	if err != nil {
		return err
	}
	_, err = w.Commit("test commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test user",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}
	cfg, err := r.Config()
	if err != nil {
		return err
	}
	cfg.Remotes["upstream"] = &config.RemoteConfig{
		Name: "upstream",
		URLs: []string{repoURL},
	}
	err = r.SetConfig(cfg)
	if err != nil {
		return err
	}
	err = r.Push(&gogit.PushOptions{
		RemoteName: "upstream",
		RemoteURL:  repoURL,
		Auth: &httpgit.BasicAuth{
			Username: gogsUser,
			Password: gogsPass,
		},
		InsecureSkipTLS: true,
	})
	if err != nil {
		return err
	}

	return nil
}

func getURL(ctx context.Context, container testcontainers.Container) (string, error) {
	mappedPort, err := container.MappedPort(ctx, "3000")
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

func verifyBundleAndAllResourcesArePresent(output io.Writer, numResources int) {
	Eventually(func() bool {
		bundle, err := cli.GetBundleFromOutput(output)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(bundle.Spec.Resources)).To(Equal(numResources))
		isSvcPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"simple/svc.yaml", bundle.Spec.Resources)
		Expect(err).NotTo(HaveOccurred())
		isDeploymentPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"simple/deployment.yaml", bundle.Spec.Resources)
		Expect(err).NotTo(HaveOccurred())

		return isSvcPresent && isDeploymentPresent
	}).Should(BeTrue())
}

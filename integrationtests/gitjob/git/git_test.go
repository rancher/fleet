package integrationtests

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/gogits/go-gogs-client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	cp "github.com/otiai10/copy"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/ssh"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/mocks"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetgit "github.com/rancher/fleet/pkg/git"
)

func init() {
	utils.DisableReaper()
}

/*
These tests use gogs for testing integration with a git server. Gogs container uses data from assets/gitserver, which
contains one user, one public repository, and another private repository. Initial commits and fingerprint are provided as consts.
*/
const (
	gogsFingerPrint = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBOLWGeeq/e1mK/zH47UeQeMtdh+NEz6j7xp5cAINcV2pPWgAsuyh5dumMv1RkC1rr0pmWekCoMnR2c4+PllRqrQ="
	gogsUser        = "test"
	gogsPass        = "pass"
	startupTimeout  = 60 * time.Second
)

var (
	gogsClient              *gogs.Client
	latestCommitPublicRepo  string
	latestCommitPrivateRepo string
	url                     string
	container               testcontainers.Container
	tmpDir                  string
)

var _ = BeforeSuite(func() {
	ctx := context.Background()
	var err error
	tmpDir, err = os.MkdirTemp("", "gogs")
	Expect(err).NotTo(HaveOccurred())

	container, url, err = createGogsContainer(ctx, tmpDir)
	Expect(err).NotTo(HaveOccurred())

	// Register cleanup handlers that the container terminates first
	// This ensures the container terminates and releases the bind mount before we try to remove tmpDir
	DeferCleanup(func() {
		if container != nil {
			cleanCtx := context.Background()

			// Gather logs for troubleshooting if test failed
			if CurrentSpecReport().Failed() {
				logs, err := container.Logs(cleanCtx)
				if err == nil {
					logContent, err := io.ReadAll(logs)
					if err == nil {
						GinkgoWriter.Printf("\n=== Container logs for Gogs ===\n%s\n", string(logContent))
					}
					_ = logs.Close()
				}
			}

			err := container.Terminate(cleanCtx)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	DeferCleanup(func() {
		if tmpDir != "" {
			err := os.RemoveAll(tmpDir)
			Expect(err).NotTo(HaveOccurred())
		}
	})
})

var _ = Describe("Git Fetch", func() {
	Describe("LatestCommit NoAuth", func() {
		var ctx context.Context
		var ctlr *gomock.Controller

		BeforeEach(func() {
			ctx = context.Background()
			ctlr = gomock.NewController(GinkgoT())
			config.Set(&config.Config{})
		})

		AfterEach(func() {
			ctlr.Finish()
		})

		type testCase struct {
			repoPath          string
			expectError       bool
			getExpectedCommit func() string
		}

		DescribeTable("repository access",
			func(tc testCase) {
				gitrepo := &v1alpha1.GitRepo{
					Spec: v1alpha1.GitRepoSpec{
						Repo:   url + tc.repoPath,
						Branch: "master",
					},
				}

				f := fleetgit.Fetch{}
				client := mocks.NewMockK8sClient(ctlr)
				// May be called multiple times, including calls to get Rancher CA bundle secrets
				client.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).
					Return(apierrors.NewNotFound(schema.GroupResource{}, "notfound")).AnyTimes()
				latestCommit, err := f.LatestCommit(ctx, gitrepo, client)

				if tc.expectError {
					Expect(err).To(HaveOccurred())
					Expect(err).To(MatchError(transport.ErrAuthenticationRequired))
					Expect(latestCommit).To(BeEmpty())
				} else {
					Expect(err).NotTo(HaveOccurred())
					Expect(latestCommit).To(Equal(tc.getExpectedCommit()))
				}
			},
			Entry("fetches the latest commit from a public repo",
				testCase{
					repoPath:          "/test/public-repo",
					expectError:       false,
					getExpectedCommit: func() string { return latestCommitPublicRepo },
				}),
			Entry("fails to fetch from a private repo without credentials",
				testCase{
					repoPath:    "/test/private-repo",
					expectError: true,
				}),
		)
	})

	Describe("LatestCommit BasicAuth", func() {
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
		})

		createBasicAuthSecret := func() *v1.Secret {
			return &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.DefaultGitCredentialsSecretName,
					Namespace: "",
				},
				Data: map[string][]byte{v1.BasicAuthUsernameKey: []byte(gogsUser), v1.BasicAuthPasswordKey: []byte(gogsPass)},
				Type: v1.SecretTypeBasicAuth,
			}
		}

		type testCase struct {
			repoPath          string
			getExpectedCommit func() string
		}

		DescribeTable("repository access with basic auth",
			func(tc testCase) {
				gitrepo := &v1alpha1.GitRepo{
					Spec: v1alpha1.GitRepoSpec{
						Repo:   url + tc.repoPath,
						Branch: "master",
					},
				}

				secret := createBasicAuthSecret()
				f := fleetgit.Fetch{}
				client := fake.NewClientBuilder().WithRuntimeObjects(secret).Build()
				latestCommit, err := f.LatestCommit(ctx, gitrepo, client)
				Expect(err).NotTo(HaveOccurred())
				Expect(latestCommit).To(Equal(tc.getExpectedCommit()))
			},
			Entry("fetches the latest commit from a public repo with basic auth",
				testCase{
					repoPath:          "/test/public-repo",
					getExpectedCommit: func() string { return latestCommitPublicRepo },
				}),
			Entry("fetches the latest commit from a private repo with basic auth",
				testCase{
					repoPath:          "/test/private-repo",
					getExpectedCommit: func() string { return latestCommitPrivateRepo },
				}),
		)
	})

	Describe("LatestCommit SSH", func() {
		var privateKey string
		var sshPort string

		BeforeEach(func() {
			ctx := context.Background()
			port, err := container.MappedPort(ctx, "22")
			Expect(err).NotTo(HaveOccurred())
			sshPort = port.Port()
		})

		// Set up SSH key once by checking if it's already initialized
		BeforeEach(func() {
			// Only create and add keys once
			if privateKey == "" {
				var err error
				privateKey, err = createAndAddKeys()
				Expect(err).NotTo(HaveOccurred())
			}
		})

		type testCase struct {
			repoPath          string
			knownHostsData    func() []byte
			getExpectedCommit func() string
		}

		DescribeTable("repository access with SSH",
			func(tc testCase) {
				ctx := context.Background()
				gitrepo := &v1alpha1.GitRepo{
					Spec: v1alpha1.GitRepoSpec{
						Repo:   "ssh://git@localhost:" + sshPort + tc.repoPath,
						Branch: "master",
					},
				}

				knownHosts := tc.knownHostsData()
				secretData := map[string][]byte{
					v1.SSHAuthPrivateKey: []byte(privateKey),
				}
				if len(knownHosts) > 0 {
					secretData["known_hosts"] = knownHosts
				}

				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.DefaultGitCredentialsSecretName,
						Namespace: gitrepo.Namespace,
					},
					Data: secretData,
					Type: v1.SecretTypeSSHAuth,
				}
				client := fake.NewClientBuilder().WithRuntimeObjects(secret).Build()
				f := fleetgit.Fetch{
					KnownHosts: mockKnownHostsGetter{
						isStrict:   false,
						knownHosts: string(knownHosts),
					},
				}
				latestCommit, err := f.LatestCommit(ctx, gitrepo, client)
				Expect(err).NotTo(HaveOccurred())
				Expect(latestCommit).To(Equal(tc.getExpectedCommit()))
			},
			Entry("fetches the latest commit from a public repo with SSH",
				testCase{
					repoPath: "/test/public-repo",
					knownHostsData: func() []byte {
						return []byte("[localhost]:" + sshPort + " " + gogsFingerPrint)
					},
					getExpectedCommit: func() string { return latestCommitPublicRepo },
				}),
			Entry("fetches the latest commit from a private repo with SSH and known hosts",
				testCase{
					repoPath: "/test/private-repo",
					knownHostsData: func() []byte {
						return []byte("[localhost]:" + sshPort + " " + gogsFingerPrint)
					},
					getExpectedCommit: func() string { return latestCommitPrivateRepo },
				}),
			Entry("fetches the latest commit from a private repo with SSH without known hosts",
				testCase{
					repoPath:          "/test/private-repo",
					knownHostsData:    func() []byte { return []byte("") },
					getExpectedCommit: func() string { return latestCommitPrivateRepo },
				}),
			Entry("fetches the latest commit from a private repo with SSH when known host URL is wrong",
				testCase{
					repoPath: "/test/private-repo",
					knownHostsData: func() []byte {
						return []byte("doesntexist " + gogsFingerPrint)
					},
					getExpectedCommit: func() string { return latestCommitPrivateRepo },
				}),
		)
	})

	Describe("LatestCommit Revision", func() {
		var ctx context.Context
		var ctlr *gomock.Controller
		var f fleetgit.Fetch
		var client client.Client

		BeforeEach(func() {
			ctx = context.Background()
			ctlr = gomock.NewController(GinkgoT())
			f = fleetgit.Fetch{}
			mockClient := mocks.NewMockK8sClient(ctlr)
			mockClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).
				Return(apierrors.NewNotFound(schema.GroupResource{}, "notfound")).AnyTimes()
			client = mockClient
		})

		AfterEach(func() {
			ctlr.Finish()
		})

		It("fails to fetch the latest commit for a nonexistent revision", func() {
			gitrepo := &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:     url + "/test/public-repo",
					Revision: "v10.0.0",
				},
			}

			latestCommit, err := f.LatestCommit(ctx, gitrepo, client)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(errors.New("commit not found for revision: v10.0.0")))
			Expect(latestCommit).To(BeEmpty())
		})

		It("succeeds when the revision is a commit", func() {
			gitrepo := &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:     url + "/test/public-repo",
					Revision: "319e76a30f012a760aa7f35d125a4eca8a2c8ba2",
				},
			}

			latestCommit, err := f.LatestCommit(ctx, gitrepo, client)
			Expect(err).NotTo(HaveOccurred())
			Expect(latestCommit).To(Equal("319e76a30f012a760aa7f35d125a4eca8a2c8ba2"))
		})

		It("succeeds when the revision is a lightweight tag", func() {
			tagCommit0, err := addRepoCommitAndTag(url+"/test/public-repo", "public-repo", "v0.0.0", "")
			Expect(err).NotTo(HaveOccurred())

			gitrepo := &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:     url + "/test/public-repo",
					Revision: "v0.0.0",
				},
			}

			latestCommit, err := f.LatestCommit(ctx, gitrepo, client)
			Expect(err).NotTo(HaveOccurred())
			Expect(latestCommit).To(Equal(tagCommit0))
		})

		It("succeeds when the revision is an annotated tag", func() {
			tagCommit1, err := addRepoCommitAndTag(url+"/test/public-repo", "public-repo", "v0.0.1", "Annotated tag v0.0.1")
			Expect(err).NotTo(HaveOccurred())

			gitrepo := &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:     url + "/test/public-repo",
					Revision: "v0.0.1",
				},
			}

			latestCommit, err := f.LatestCommit(ctx, gitrepo, client)
			Expect(err).NotTo(HaveOccurred())
			Expect(latestCommit).To(Equal(tagCommit1))
		})
	})
})

func TestGitFetch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Git Fetch Suite")
}

func createGogsContainer(ctx context.Context, tmpDir string) (testcontainers.Container, string, error) {
	err := cp.Copy("../assets/gitserver", tmpDir)
	if err != nil {
		return nil, "", err
	}

	err = os.Chmod(filepath.Join(tmpDir, "git", ".ssh"), 0700)
	if err != nil {
		return nil, "", fmt.Errorf("failed to change permissions for .ssh directory: %w", err)
	}

	req := testcontainers.ContainerRequest{
		Image:        "gogs/gogs:0.13",
		ExposedPorts: []string{"3000/tcp", "22/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForHTTP("/").WithPort("3000/tcp").WithStartupTimeout(startupTimeout),
			wait.ForListeningPort("22/tcp").WithStartupTimeout(startupTimeout),
		),
		HostConfigModifier: func(hostConfig *dockercontainer.HostConfig) {
			// Use Binds instead of Mounts to support SELinux relabeling with :z flag
			// The :z flag allows the bind mount to be shared between containers and
			// automatically relabels the content for SELinux
			hostConfig.Binds = []string{tmpDir + ":/data:z"}
		},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})

	if err != nil {
		return nil, "", err
	}

	url, err := getURL(ctx, container)
	if err != nil {
		return nil, "", err
	}

	c := gogs.NewClient(url, "")
	token, err := c.CreateAccessToken(gogsUser, gogsPass, gogs.CreateAccessTokenOption{
		Name: "test",
	})
	if err != nil {
		return nil, "", err
	}

	gogsClient = gogs.NewClient(url, token.Sha1)
	latestCommitPublicRepo, err = initRepo(url, "public-repo", false)
	if err != nil {
		return nil, "", err
	}
	if latestCommitPublicRepo == "" {
		return nil, "", fmt.Errorf("latestCommitPublicRepo is empty after initRepo")
	}
	latestCommitPrivateRepo, err = initRepo(url, "private-repo", true)
	if err != nil {
		return nil, "", err
	}
	if latestCommitPrivateRepo == "" {
		return nil, "", fmt.Errorf("latestCommitPrivateRepo is empty after initRepo")
	}

	return container, url, nil
}

// initRepo creates a git repo and adds an initial commit.
func initRepo(url string, name string, private bool) (string, error) {
	// create repo
	_, err := gogsClient.CreateRepo(gogs.CreateRepoOption{
		Name:    name,
		Private: private,
	})
	if err != nil {
		return "", err
	}
	repoURL := url + "/" + gogsUser + "/" + name

	// add initial commit
	tmp, err := os.MkdirTemp("", name)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	if _, err = gogit.PlainInit(tmp, false); err != nil {
		return "", err
	}

	r, err := gogit.PlainOpen(tmp)
	if err != nil {
		return "", err
	}
	filename := filepath.Join(tmp, "example-git-file")
	err = os.WriteFile(filename, []byte("test"), 0600)
	if err != nil {
		return "", err
	}
	w, err := r.Worktree()
	if err != nil {
		return "", err
	}
	_, err = w.Add("example-git-file")
	if err != nil {
		return "", err
	}
	commit, err := w.Commit("test commit", &gogit.CommitOptions{
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
	cfg.Remotes["upstream"] = &gitconfig.RemoteConfig{
		Name: "upstream",
		URLs: []string{repoURL},
	}
	err = r.SetConfig(cfg)
	if err != nil {
		return "", err
	}
	err = r.Push(&gogit.PushOptions{
		RemoteName: "upstream",
		RemoteURL:  repoURL,
		Auth: &httpgit.BasicAuth{
			Username: gogsUser,
			Password: gogsPass,
		},
	})
	if err != nil {
		return "", err
	}

	return commit.String(), nil
}

func addRepoCommitAndTag(url string, name string, tag string, tagMessage string) (string, error) {
	tmp, err := os.MkdirTemp("", name)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	r, err := gogit.PlainClone(tmp, false, &gogit.CloneOptions{
		URL:               url,
		RecurseSubmodules: gogit.DefaultSubmoduleRecursionDepth,
	})
	if err != nil {
		return "", err
	}
	filename := filepath.Join(tmp, "example-git-file")
	err = os.WriteFile(filename, []byte("test"+tag), 0600)
	if err != nil {
		return "", err
	}
	w, err := r.Worktree()
	if err != nil {
		return "", err
	}
	_, err = w.Add("example-git-file")
	if err != nil {
		return "", err
	}
	commit, err := w.Commit("test commit for tag "+tag, &gogit.CommitOptions{
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
	cfg.Remotes["upstream"] = &gitconfig.RemoteConfig{
		Name: "upstream",
		URLs: []string{url},
	}
	err = r.SetConfig(cfg)
	if err != nil {
		return "", err
	}
	err = r.Push(&gogit.PushOptions{
		RemoteName: "upstream",
		RemoteURL:  url,
		Auth: &httpgit.BasicAuth{
			Username: gogsUser,
			Password: gogsPass,
		},
	})
	if err != nil {
		return "", err
	}

	if tagMessage == "" {
		// lightweight tag
		_, err = r.CreateTag(tag, commit, nil)
	} else {
		// annotated tag
		_, err = r.CreateTag(tag, commit, &gogit.CreateTagOptions{
			Message: tagMessage,
			Tagger: &object.Signature{
				Name:  "Test user",
				Email: "test@test.com",
				When:  time.Now(),
			},
		})
	}
	if err != nil {
		return "", err
	}

	err = r.Push(&gogit.PushOptions{
		RemoteName: "upstream",
		RemoteURL:  url,
		Auth: &httpgit.BasicAuth{
			Username: gogsUser,
			Password: gogsPass,
		},
		RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec("refs/tags/*:refs/tags/*")},
	})
	if err != nil {
		return "", nil
	}

	return commit.String(), nil
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
	url := "http://" + host + ":" + mappedPort.Port()

	return url, nil
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

type mockKnownHostsGetter struct {
	isStrict   bool
	knownHosts string
}

func (m mockKnownHostsGetter) IsStrict() bool {
	return m.isStrict
}

func (m mockKnownHostsGetter) GetWithSecret(_ context.Context, _ client.Client, _ *v1.Secret) (string, error) {
	return m.knownHosts, nil
}

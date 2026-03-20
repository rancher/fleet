package bundlereader_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gogs "github.com/gogits/go-gogs-client"
	cp "github.com/otiai10/copy"
	"github.com/testcontainers/testcontainers-go"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/bundlereader"
)

func init() {
	utils.DisableReaper()
}

const (
	testRepoName = "test-repo"
	timeout      = 240 * time.Second
	interval     = 10 * time.Second
)

var (
	gogsClient   *gogs.Client
	gogsCABundle []byte
)

var _ = Describe("GetContent fetches files from a git repository", Label("networking"), Ordered, func() {
	var container testcontainers.Container

	BeforeAll(func() {
		// Suppress interactive git prompts when running against the go-getter
		// implementation.
		// go-git never reads these env vars, so the later go-git based
		// implementation should be unaffected by those.
		DeferCleanup(os.Unsetenv, "GIT_TERMINAL_PROMPT")
		DeferCleanup(os.Unsetenv, "GIT_SSH_COMMAND")
		os.Setenv("GIT_TERMINAL_PROMPT", "0")
		os.Setenv("GIT_SSH_COMMAND", "ssh -o StrictHostKeyChecking=accept-new")

		var err error
		container, gogsCABundle, gogsClient, err = utils.CreateGogsContainerWithHTTPS(context.Background(), "../gitcloner/assets/gitserver")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			if container != nil {
				_ = container.Terminate(context.Background())
			}
		})
	})

	When("fetching from an https git repo", func() {
		var (
			repoName  string
			httpsBase string
		)

		JustBeforeEach(func() {
			var err error
			httpsBase, err = utils.GetGogsHTTPSURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			repoName, _, err = createRepo(httpsBase, false)
			Expect(err).NotTo(HaveOccurred())
		})

		JustAfterEach(func() {
			Expect(gogsClient.DeleteRepo(utils.GogsUser, repoName)).To(Succeed())
		})

		When("insecureSkipVerify is true", func() {
			It("returns the repository files", func() {
				source := fmt.Sprintf("git::%s/%s/%s", httpsBase, utils.GogsUser, repoName)
				files, err := bundlereader.GetContent(
					context.Background(), GinkgoT().TempDir(), source, "",
					bundlereader.Auth{InsecureSkipVerify: true}, false, nil,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(files).To(HaveKey("README.md"))
				Expect(files).To(HaveKey(filepath.Join("subdir", "config.yaml")))
			})
		})

		When("no TLS configuration is provided", func() {
			It("fails with a certificate error", func() {
				source := fmt.Sprintf("git::%s/%s/%s", httpsBase, utils.GogsUser, repoName)
				_, err := bundlereader.GetContent(
					context.Background(), GinkgoT().TempDir(), source, "",
					bundlereader.Auth{}, false, nil,
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("certificate"))
			})
		})

		When("the repo is private and credentials are embedded in the URL", func() {
			var privateRepoName string

			JustBeforeEach(func() {
				var err error
				privateRepoName, _, err = createPrivateRepo(httpsBase)
				Expect(err).NotTo(HaveOccurred())
			})

			JustAfterEach(func() {
				Expect(gogsClient.DeleteRepo(utils.GogsUser, privateRepoName)).To(Succeed())
			})

			It("returns the repository files when credentials are embedded in URL", func() {
				u, err := url.Parse(fmt.Sprintf("%s/%s/%s", httpsBase, utils.GogsUser, privateRepoName))
				Expect(err).NotTo(HaveOccurred())
				u.User = url.UserPassword(utils.GogsUser, utils.GogsPass)
				source := "git::" + u.String()

				files, err := bundlereader.GetContent(
					context.Background(), GinkgoT().TempDir(), source, "",
					bundlereader.Auth{InsecureSkipVerify: true}, false, nil,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(files).To(HaveKey("README.md"))
			})

			It("fails when no credentials are provided", func() {
				source := fmt.Sprintf("git::%s/%s/%s", httpsBase, utils.GogsUser, privateRepoName)
				_, err := bundlereader.GetContent(
					context.Background(), GinkgoT().TempDir(), source, "",
					bundlereader.Auth{InsecureSkipVerify: true}, false, nil,
				)
				Expect(err).To(HaveOccurred())
			})
		})

		When("fetching a specific branch via ?ref=", func() {
			var branchRepoName string

			JustBeforeEach(func() {
				var err error
				branchRepoName, _, err = createRepoWithBranch(httpsBase)
				Expect(err).NotTo(HaveOccurred())
			})

			JustAfterEach(func() {
				Expect(gogsClient.DeleteRepo(utils.GogsUser, branchRepoName)).To(Succeed())
			})

			It("returns files from the specified branch", func() {
				source := fmt.Sprintf("git::%s/%s/%s?ref=feature", httpsBase, utils.GogsUser, branchRepoName)
				files, err := bundlereader.GetContent(
					context.Background(), GinkgoT().TempDir(), source, "",
					bundlereader.Auth{InsecureSkipVerify: true}, false, nil,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(files).To(HaveKey("feature.yaml"))
				Expect(files).NotTo(HaveKey("README.md"))
			})
		})

		When("fetching a subdirectory via // path", func() {
			It("returns only the files in the subdirectory", func() {
				source := fmt.Sprintf("git::%s/%s/%s//subdir", httpsBase, utils.GogsUser, repoName)
				files, err := bundlereader.GetContent(
					context.Background(), GinkgoT().TempDir(), source, "",
					bundlereader.Auth{InsecureSkipVerify: true}, false, nil,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(files).To(HaveKey("config.yaml"))
				Expect(files).NotTo(HaveKey("README.md"))
			})
		})
	})

	When("fetching from an ssh git repo", Label("ssh"), func() {
		var (
			repoName   string
			httpsBase  string
			sshBase    string
			tmpKeyFile string
		)

		JustBeforeEach(func() {
			var err error
			httpsBase, err = utils.GetGogsHTTPSURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())
			sshBase, err = utils.GetGogsSSHURL(context.Background(), container)
			Expect(err).NotTo(HaveOccurred())

			repoName, _, err = createPrivateRepo(httpsBase)
			Expect(err).NotTo(HaveOccurred())

			publicKey, privateKey, err := utils.MakeSSHKeyPair()
			Expect(err).NotTo(HaveOccurred())
			_, err = gogsClient.CreatePublicKey(gogs.CreateKeyOption{Title: "test", Key: publicKey})
			Expect(err).NotTo(HaveOccurred())

			tmpKeyFile = filepath.Join(GinkgoT().TempDir(), "id_rsa")
			Expect(os.WriteFile(tmpKeyFile, []byte(privateKey), 0600)).To(Succeed())
		})

		JustAfterEach(func() {
			Expect(gogsClient.DeleteRepo(utils.GogsUser, repoName)).To(Succeed())
			keys, err := gogsClient.ListMyPublicKeys()
			Expect(err).NotTo(HaveOccurred())
			for _, key := range keys {
				_ = gogsClient.DeletePublicKey(key.ID)
			}
		})

		It("returns the repository files when an SSH private key is provided", func() {
			privateKey, err := os.ReadFile(tmpKeyFile)
			Expect(err).NotTo(HaveOccurred())

			source := fmt.Sprintf("%s/%s/%s", sshBase, utils.GogsUser, repoName)
			Eventually(func() error {
				_, err = bundlereader.GetContent(
					context.Background(), GinkgoT().TempDir(), source, "",
					bundlereader.Auth{SSHPrivateKey: privateKey}, false, nil,
				)
				return err
			}, timeout, interval).Should(Succeed())

			files, err := bundlereader.GetContent(
				context.Background(), GinkgoT().TempDir(), source, "",
				bundlereader.Auth{SSHPrivateKey: privateKey}, false, nil,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveKey("README.md"))
		})
	})
})

// createRepo creates a public or private git repo in gogs, pushes test files, and returns its name and commit SHA.
func createRepo(baseURL string, private bool) (string, string, error) {
	repoName := fmt.Sprintf("%s%d", testRepoName, time.Now().UTC().UnixMicro())
	_, err := gogsClient.CreateRepo(gogs.CreateRepoOption{Name: repoName, Private: private})
	if err != nil {
		return "", "", err
	}
	commit, err := pushAssets(baseURL, repoName, "../gitcloner/assets/repo")
	return repoName, commit, err
}

// createPrivateRepo creates a private git repo in gogs.
func createPrivateRepo(baseURL string) (string, string, error) {
	return createRepo(baseURL, true)
}

// createRepoWithBranch creates a repo with a "master" branch (from assets) and an additional "feature" branch.
func createRepoWithBranch(baseURL string) (string, string, error) {
	repoName := fmt.Sprintf("%s%d", testRepoName, time.Now().UTC().UnixMicro())
	_, err := gogsClient.CreateRepo(gogs.CreateRepoOption{Name: repoName, Private: false})
	if err != nil {
		return "", "", err
	}

	_, err = pushAssets(baseURL, repoName, "../gitcloner/assets/repo")
	if err != nil {
		return "", "", err
	}

	repoURL := fmt.Sprintf("%s/%s/%s", baseURL, utils.GogsUser, repoName)
	tmp, err := os.MkdirTemp("", repoName)
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	r, err := gogit.PlainClone(tmp, false, &gogit.CloneOptions{
		URL:             repoURL,
		InsecureSkipTLS: true,
		Auth:            &httpgit.BasicAuth{Username: utils.GogsUser, Password: utils.GogsPass},
	})
	if err != nil {
		return "", "", fmt.Errorf("clone for branch creation: %w", err)
	}
	w, err := r.Worktree()
	if err != nil {
		return "", "", err
	}
	if err := w.Checkout(&gogit.CheckoutOptions{Branch: "refs/heads/feature", Create: true}); err != nil {
		return "", "", fmt.Errorf("checkout feature branch: %w", err)
	}

	// Remove master files and add a feature-specific one so the branch is distinct.
	_ = os.Remove(filepath.Join(tmp, "README.md"))
	if err := os.WriteFile(filepath.Join(tmp, "feature.yaml"), []byte("feature: true\n"), 0600); err != nil {
		return "", "", err
	}
	if _, err := w.Add("."); err != nil {
		return "", "", err
	}
	commit, err := w.Commit("feature commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		return "", "", err
	}
	if err := r.Push(&gogit.PushOptions{
		Auth:            &httpgit.BasicAuth{Username: utils.GogsUser, Password: utils.GogsPass},
		InsecureSkipTLS: true,
		RefSpecs:        []config.RefSpec{"refs/heads/feature:refs/heads/feature"},
	}); err != nil {
		return "", "", fmt.Errorf("push feature branch: %w", err)
	}

	return repoName, commit.String(), nil
}

// pushAssets initialises a local repo from path, adds all files, and pushes to gogs.
// It also creates a subdir/config.yaml file to enable subdirectory tests.
func pushAssets(baseURL, repoName, path string) (string, error) {
	repoURL := fmt.Sprintf("%s/%s/%s", baseURL, utils.GogsUser, repoName)
	tmp, err := os.MkdirTemp("", repoName)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	r, err := gogit.PlainInit(tmp, false)
	if err != nil {
		return "", err
	}
	w, err := r.Worktree()
	if err != nil {
		return "", err
	}
	if err := cp.Copy(path, tmp); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(tmp, "subdir"), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, "subdir", "config.yaml"), []byte("key: value\n"), 0600); err != nil {
		return "", err
	}
	if _, err := w.Add("."); err != nil {
		return "", err
	}
	commit, err := w.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		return "", err
	}
	cfg, err := r.Config()
	if err != nil {
		return "", err
	}
	cfg.Remotes["upstream"] = &config.RemoteConfig{Name: "upstream", URLs: []string{repoURL}}
	if err := r.SetConfig(cfg); err != nil {
		return "", err
	}
	if err := r.Push(&gogit.PushOptions{
		RemoteName:      "upstream",
		RemoteURL:       repoURL,
		Auth:            &httpgit.BasicAuth{Username: utils.GogsUser, Password: utils.GogsPass},
		InsecureSkipTLS: true,
	}); err != nil {
		return "", err
	}
	return commit.String(), nil
}

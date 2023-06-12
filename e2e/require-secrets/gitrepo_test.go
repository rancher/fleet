package require_secrets

// These test cases rely on an external git server, hence they cannot be run locally nor against PRs.
// For tests relying on an internal git server, see `e2e/single-cluster`.

import (
	"bytes"
	"os"
	"path"

	"github.com/go-git/go-git/v5"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Git Repo", func() {
	var (
		tmpdir  string
		repodir string
		k       kubectl.Command
		gh      *githelper.Git
		repo    *git.Repository
	)

	replace := func(path string, s string, r string) {
		b, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())

		b = bytes.ReplaceAll(b, []byte(s), []byte(r))

		err = os.WriteFile(path, b, 0644)
		Expect(err).ToNot(HaveOccurred())
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		gh = githelper.NewSSH()

		out, err := k.Create(
			"secret", "generic", "git-auth", "--type", "kubernetes.io/ssh-auth",
			"--from-file=ssh-privatekey="+os.Getenv("GIT_SSH_KEY"),
			"--from-file=ssh-publickey="+os.Getenv("GIT_SSH_PUBKEY"),
		)
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), struct {
			Repo   string
			Branch string
		}{
			gh.GetURL(),
			gh.Branch,
		})
		Expect(err).ToNot(HaveOccurred(), out)

		tmpdir, _ = os.MkdirTemp("", "fleet-")
		repodir = path.Join(tmpdir, "repo")
		repo, err = gh.Create(repodir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
		Expect(err).ToNot(HaveOccurred())

	})

	AfterEach(func() {
		os.RemoveAll(tmpdir)
		_, _ = k.Delete("secret", "git-auth")
		_, _ = k.Delete("gitrepo", "gitrepo-test")
	})

	When("updating a git repository", func() {
		It("updates the deployment", func() {
			By("checking the pod exists")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("pods")
				return out
			}).Should(ContainSubstring("sleeper-"))

			By("updating the git repository")
			replace(path.Join(repodir, "examples", "Chart.yaml"), "0.1.0", "0.2.0")
			replace(path.Join(repodir, "examples", "templates", "deployment.yaml"), "name: sleeper", "name: newsleep")

			commit, err := gh.Update(repo)
			Expect(err).ToNot(HaveOccurred())

			By("checking for the updated commit hash in gitrepo")
			Eventually(func() string {
				out, _ := k.Get("gitrepo", "gitrepo-test", "-o", "yaml")
				return out
			}).Should(ContainSubstring("commit: " + commit))

			By("checking the deployment's new name")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("deployments")
				return out
			}).Should(ContainSubstring("newsleep"))

		})
	})
})

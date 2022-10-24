package require_secrets

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
		gh = githelper.New()
		tmpdir, _ = os.MkdirTemp("", "fleet-")

		out, err := k.Create(
			"secret", "generic", "git-auth", "--type", "kubernetes.io/ssh-auth",
			"--from-file=ssh-privatekey="+gh.SSHKey,
			"--from-file=ssh-publickey="+gh.SSHPubKey,
		)
		Expect(err).ToNot(HaveOccurred(), out)

		known := path.Join(tmpdir, "known_hosts")
		os.Setenv("SSH_KNOWN_HOSTS", known)
		out, err = gh.UpdateKnownHosts(known)
		Expect(err).ToNot(HaveOccurred(), out)

		repodir = path.Join(tmpdir, "repo")
		repo, err = gh.Create(repodir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
		Expect(err).ToNot(HaveOccurred())

		gitrepo := path.Join(tmpdir, "gitrepo.yaml")
		err = testenv.Template(gitrepo, testenv.AssetPath("gitrepo/gitrepo.yaml"), struct {
			Repo   string
			Branch string
		}{
			gh.URL,
			gh.Branch,
		})
		Expect(err).ToNot(HaveOccurred())

		out, err = k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)
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

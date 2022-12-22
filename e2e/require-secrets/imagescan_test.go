package require_secrets

import (
	"os"
	"path"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Image Scan", func() {
	var (
		tmpdir  string
		repodir string
		k       kubectl.Command
		gh      *githelper.Git
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		gh = githelper.New()

		out, err := k.Create(
			"secret", "generic", "git-auth", "--type", "kubernetes.io/ssh-auth",
			"--from-file=ssh-privatekey="+gh.SSHKey,
			"--from-file=ssh-publickey="+gh.SSHPubKey,
			"--from-file=known_hosts="+knownHostsPath,
		)
		Expect(err).ToNot(HaveOccurred(), out)

		os.Setenv("GIT_REPO_BRANCH", "imagescan")

		tmpdir, _ = os.MkdirTemp("", "fleet-")
		repodir = path.Join(tmpdir, "repo")
		_, err = gh.Create(repodir, testenv.AssetPath("imagescan/repo"), "examples")
		Expect(err).ToNot(HaveOccurred())

		gitrepo := path.Join(tmpdir, "gitrepo.yaml")
		err = testenv.Template(gitrepo, testenv.AssetPath("imagescan/imagescan.yaml"), struct {
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
		_, _ = k.Delete("gitrepo", "imagescan")
	})

	When("update docker reference in git via image scan", func() {
		It("updates the docker reference", func() {
			By("checking the deployment exists")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("pods")
				return out
			}).Should(ContainSubstring("nginx-"))

			By("checking for the original docker reference")
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(`image: nginx:latest # {"\$imagescan": "test-scan:digest"}`))

			By("checking for the updated docker reference")
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(`image: index\.docker\.io\/library\/nginx:[0-9][.0-9]*@sha256:[0-9a-f]{64} # {"\$imagescan": "test-scan:digest"}`))

		})
	})
})

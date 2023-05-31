package singlecluster_test

import (
	"os"
	"path"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Image Scan", func() {
	var (
		tmpdir   string
		clonedir string
		k        kubectl.Command
		gh       *githelper.Git
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)

		// Create git server
		out, err := k.Apply("-f", testenv.AssetPath("gitrepo/nginx_deployment.yaml"))
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Apply("-f", testenv.AssetPath("gitrepo/nginx_service.yaml"))
		Expect(err).ToNot(HaveOccurred(), out)

		time.Sleep(10 * time.Second) // give git server time to spin up

		ip, err := githelper.GetExternalRepoIP(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.New(ip, false)
		gh.Branch = "imagescan"

		tmpdir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpdir, "clone")
		_, err = gh.Create(clonedir, testenv.AssetPath("imagescan/repo"), "examples")
		Expect(err).ToNot(HaveOccurred())

		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host, err := githelper.BuildGitHostname(env.Namespace)
		Expect(err).ToNot(HaveOccurred())

		inClusterRepoURL := gh.GetInClusterURL(host, port, repoName)

		gitrepo := path.Join(tmpdir, "gitrepo.yaml")
		err = testenv.Template(gitrepo, testenv.AssetPath("imagescan/imagescan.yaml"), struct {
			Repo   string
			Branch string
		}{
			inClusterRepoURL,
			gh.Branch,
		})
		Expect(err).ToNot(HaveOccurred())

		out, err = k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		os.RemoveAll(tmpdir)
		_, _ = k.Delete("gitrepo", "imagescan")
		_, _ = k.Delete("deployment", "git-server")
		_, _ = k.Delete("service", "git-service")
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
				MatchRegexp(`image: public\.ecr\.aws\/nginx\/nginx:latest # {"\$imagescan": "test-scan:digest"}`))

			By("checking for the updated docker reference")
			Eventually(func() string {
				out, _ := k.Get("bundles", "imagescan-examples", "-o", "yaml")
				return out
			}).Should(
				MatchRegexp(`image: public\.ecr\.aws\/nginx\/nginx:[0-9][.0-9]*@sha256:[0-9a-f]{64} # {"\$imagescan": "test-scan:digest"}`))

		})
	})
})

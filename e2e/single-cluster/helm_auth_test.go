package singlecluster_test

import (
	"fmt"
	"os"
	"path"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Single Cluster Examples", Label("infra-setup"), func() {
	var (
		gitRepoPath     string
		tmpdir          string
		k               kubectl.Command
		gh              *githelper.Git
		targetNamespace string
	)

	JustBeforeEach(func() {
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host, err := githelper.BuildGitHostname(env.Namespace)
		Expect(err).ToNot(HaveOccurred())

		inClusterRepoURL := gh.GetInClusterURL(host, port, repoName)

		gitrepo := path.Join(tmpdir, "gitrepo.yaml")
		err = testenv.Template(gitrepo, testenv.AssetPath("single-cluster/helm-with-auth.yaml"), struct {
			Repo       string
			Path       string
			SecretName string
		}{
			inClusterRepoURL,
			gitRepoPath,
			"helm-secret",
		})
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("creating a gitrepo resource", func() {
		Context("containing a private OCI-based helm chart", Label("oci-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "oci-with-auth"
				targetNamespace = fmt.Sprintf("fleet-helm-%s", gitRepoPath)
				k = env.Kubectl.Namespace(env.Namespace)

				tmpdir, gh = setupGitRepo(gitRepoPath, repoName, port)
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace(targetNamespace).Get("pods", "--field-selector=status.phase==Running")
					return out
				}).Should(ContainSubstring("sleeper-"))
			})
		})
		Context("containing a private HTTP-based helm chart with repo path", Label("helm-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "http-with-auth-repo-path"
				targetNamespace = fmt.Sprintf("fleet-helm-%s", gitRepoPath)
				k = env.Kubectl.Namespace(env.Namespace)

				tmpdir, gh = setupGitRepo(gitRepoPath, repoName, port)
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace(targetNamespace).Get("pods", "--field-selector=status.phase==Running")
					return out
				}).Should(ContainSubstring("sleeper-"))
			})
		})
		Context("containing a private HTTP-based helm chart with chart path", Label("helm-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "http-with-auth-chart-path"
				targetNamespace = fmt.Sprintf("fleet-helm-%s", gitRepoPath)
				k = env.Kubectl.Namespace(env.Namespace)

				tmpdir, gh = setupGitRepo(gitRepoPath, repoName, port)
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace(targetNamespace).Get("pods", "--field-selector=status.phase==Running")
					return out
				}).Should(ContainSubstring("sleeper-"))
			})
		})
	})

	AfterEach(func() {
		os.RemoveAll(tmpdir)

		_, _ = k.Delete("gitrepo", "helm")
		_, _ = k.Delete("ns", targetNamespace, "--wait=false")
	})
})

// setupGitRepo creates a local clone with data from repoPath and pushes it to that same path on the git server,
// within a common repository named `repo`.
func setupGitRepo(repoPath, repo string, port int) (tmpdir string, gh *githelper.Git) {
	addr, err := githelper.GetExternalRepoAddr(env, port, repo)
	Expect(err).ToNot(HaveOccurred())
	Expect(addr).ToNot(HaveLen(0))

	gh = githelper.NewHTTP(addr)

	tmpdir, _ = os.MkdirTemp("", "fleet-")
	clonedir := path.Join(tmpdir, "clone")
	_, err = gh.Create(clonedir, path.Join(testenv.AssetPath("helm/repo"), repoPath), repoPath)
	Expect(err).ToNot(HaveOccurred())

	return tmpdir, gh
}

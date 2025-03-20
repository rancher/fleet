package singlecluster_test

import (
	"fmt"
	"os"
	"path"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitRepo using Helm chart with auth", Label("infra-setup"), func() {
	var (
		gitRepoPath     string
		tmpdir          string
		k               kubectl.Command
		gh              *githelper.Git
		targetNamespace string
		helmSecretName  string
	)

	JustBeforeEach(func() {
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host := githelper.BuildGitHostname()

		inClusterRepoURL := gh.GetInClusterURL(host, port, repoName)

		gitrepo := path.Join(tmpdir, "gitrepo.yaml")
		err := testenv.Template(gitrepo, testenv.AssetPath("single-cluster/helm-with-auth.yaml"), struct {
			Repo       string
			Path       string
			SecretName string
		}{
			inClusterRepoURL,
			gitRepoPath,
			helmSecretName,
		})
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("creating a gitrepo resource", func() {
		Context("containing a private OCI-based helm chart", Label("oci-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "oci-with-auth"
				helmSecretName = "helm-secret"
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
		Context("containing a private HTTP-based helm chart with repo path and no CA bundle in secret", Label("helm-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "http-with-auth-repo-path"
				helmSecretName = "helm-secret-no-ca"
				targetNamespace = fmt.Sprintf("fleet-helm-%s", gitRepoPath)
				k = env.Kubectl.Namespace(env.Namespace)

				// No CA bundle in this secret
				out, err := k.Create(
					"secret", "generic", helmSecretName,
					"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
					"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
				)
				Expect(err).ToNot(HaveOccurred(), out)
				tmpdir, gh = setupGitRepo(gitRepoPath, repoName, port)

				DeferCleanup(func() {
					_, _ = k.Delete("secret", helmSecretName)
				})
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace(targetNamespace).Get("pods", "--field-selector=status.phase==Running")
					return out
				}).Should(ContainSubstring("sleeper-"))

				// Force-update the GitRepo and check that mount points are not altered
				out, err := k.Patch(
					"gitrepo",
					"helm",
					"--type=merge",
					"-p",
					// Use a deliberately larger number, not expected to have been reached yet.
					`{"spec":{"forceSyncGeneration": 42}}`)
				Expect(err).ToNot(HaveOccurred(), out)

				Consistently(func(g Gomega) {
					out, err := k.Get(
						"gitrepo",
						"helm",
						"-o",
						`jsonpath='{range .items[*]}{.status.conditions[?(@.type=="Stalled")].message}'`,
					)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(out).ToNot(ContainSubstring("signed by unknown authority"))
				}, 10*time.Second, 1*time.Second).Should(Succeed())
			})
		})
		Context("containing a private HTTP-based helm chart with repo path", Label("helm-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "http-with-auth-repo-path"
				helmSecretName = "helm-secret"
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
				helmSecretName = "helm-secret"
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
// nolint: unparam // repo and port are always fed with the same value - for now.
func setupGitRepo(repoPath, repo string, port int) (tmpdir string, gh *githelper.Git) {
	addr, err := githelper.GetExternalRepoAddr(env, port, repo)
	Expect(err).ToNot(HaveOccurred())
	Expect(addr).ToNot(BeEmpty())

	gh = githelper.NewHTTP(addr)

	tmpdir, _ = os.MkdirTemp("", "fleet-")
	clonedir := path.Join(tmpdir, "clone")
	_, err = gh.Create(clonedir, path.Join(testenv.AssetPath("helm/repo"), repoPath), repoPath)
	Expect(err).ToNot(HaveOccurred())

	return tmpdir, gh
}

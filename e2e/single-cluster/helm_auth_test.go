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
		Context("containing a private OCI-based helm chart and setting insecureSkipVerify to false", Label("oci-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "oci-with-auth-external"
				helmSecretName = "helm-secret-no-insecure"
				targetNamespace = "fleet-helm-oci-with-auth"
				k = env.Kubectl.Namespace(env.Namespace)

				out, err := k.Create(
					"secret", "generic", helmSecretName,
					"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
					"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
					"--from-literal=insecureSkipVerify=false",
				)
				Expect(err).ToNot(HaveOccurred(), out)

				// create the gitrepo content on the fly, to set the external IP
				// of the load balancer so it complains about the certificate.
				repoBaseDir := createFleetYAMLWithExternalIP(k, gitRepoPath)
				tmpdir, gh = setupGitRepo(repoBaseDir, gitRepoPath, repoName, port)

				DeferCleanup(func() {
					_, _ = k.Delete("secret", helmSecretName)
				})
			})

			It("fails to deploy the chart", func() {
				By("checking that there is a pod in Failed state with name helm-*")
				Eventually(func() string {
					out, _ := k.Get("pods", "--field-selector=status.phase==Failed")
					return out
				}).Should(ContainSubstring("helm-"))
				By("checking that the gitrepo reflects the expected error")
				Eventually(func(g Gomega) {
					status := getGitRepoStatus(g, k, "helm")
					stalledMessage := ""
					stalledFound := false
					for _, cond := range status.Conditions {
						if cond.Type == "Stalled" {
							stalledMessage = cond.Message
							stalledFound = true
							break
						}
					}
					g.Expect(stalledFound).To(BeTrue())
					g.Expect(stalledMessage).To(ContainSubstring("failed to verify certificate"))
				}).Should(Succeed())
			})
		})

		Context("containing a private OCI-based helm chart and setting insecureSkipVerify to true", Label("oci-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "oci-with-auth-external"
				helmSecretName = "helm-secret-insecure"
				targetNamespace = "fleet-helm-oci-with-auth"
				k = env.Kubectl.Namespace(env.Namespace)

				out, err := k.Create(
					"secret", "generic", helmSecretName,
					"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
					"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
					"--from-literal=insecureSkipVerify=true",
				)
				Expect(err).ToNot(HaveOccurred(), out)

				// create the gitrepo content on the fly, to set the external IP
				// of the load balancer so it complains about the certificate.
				repoBaseDir := createFleetYAMLWithExternalIP(k, gitRepoPath)
				tmpdir, gh = setupGitRepo(repoBaseDir, gitRepoPath, repoName, port)

				DeferCleanup(func() {
					_, _ = k.Delete("secret", helmSecretName)
				})
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace(targetNamespace).Get("pods", "--field-selector=status.phase==Running")
					return out
				}).Should(ContainSubstring("sleeper-"))
			})
		})

		Context("containing a private OCI-based helm chart", Label("oci-registry"), func() {
			BeforeEach(func() {
				gitRepoPath = "oci-with-auth"
				helmSecretName = "helm-secret"
				targetNamespace = fmt.Sprintf("fleet-helm-%s", gitRepoPath)
				k = env.Kubectl.Namespace(env.Namespace)

				tmpdir, gh = setupGitRepo(testenv.AssetPath("helm/repo"), gitRepoPath, repoName, port)
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
				tmpdir, gh = setupGitRepo(testenv.AssetPath("helm/repo"), gitRepoPath, repoName, port)

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

				tmpdir, gh = setupGitRepo(testenv.AssetPath("helm/repo"), gitRepoPath, repoName, port)
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

				tmpdir, gh = setupGitRepo(testenv.AssetPath("helm/repo"), gitRepoPath, repoName, port)
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
//
//nolint:unparam // repo and port are always fed with the same value - for now.
func setupGitRepo(basePath, repoPath, repo string, port int) (tmpdir string, gh *githelper.Git) {
	addr, err := githelper.GetExternalRepoAddr(env, port, repo)
	Expect(err).ToNot(HaveOccurred())
	Expect(addr).ToNot(BeEmpty())

	gh = githelper.NewHTTP(addr)

	tmpdir = GinkgoT().TempDir()
	clonedir := path.Join(tmpdir, "clone")

	_, err = gh.Create(clonedir, path.Join(basePath, repoPath), repoPath)
	Expect(err).ToNot(HaveOccurred())

	return tmpdir, gh
}

func createFleetYAMLWithExternalIP(k kubectl.Command, gitRepoPath string) string {
	tmpFolder := GinkgoT().TempDir()
	ip := ""
	ks := k.Namespace("")
	Eventually(func() string {
		ip, _ = ks.Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
		return ip
	}).Should(MatchRegexp(`^(\d{1,3}\.){3}\d{1,3}$`))

	fleetYAML := `namespace: fleet-helm-oci-with-auth
helm:
  releaseName: sleeper-chart
  chart: "oci://%s:8082/sleeper-chart"
  version: "0.1.0"
  force: false
  timeoutSeconds: 0
  values:
    replicas: 2`

	gitrepoFolder := path.Join(tmpFolder, gitRepoPath)
	err := os.MkdirAll(gitrepoFolder, 0755)
	Expect(err).ToNot(HaveOccurred())

	fleetYamlPath := path.Join(gitrepoFolder, "fleet.yaml")
	err = os.WriteFile(fleetYamlPath, []byte(fmt.Sprintf(fleetYAML, ip)), 0644)
	Expect(err).ToNot(HaveOccurred())

	return tmpFolder
}

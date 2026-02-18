package singlecluster_test

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("Helm v4 Null Field Drift Detection", Label("infra-setup"), func() {
	var (
		tmpDir           string
		clonedir         string
		k                kubectl.Command
		gh               *githelper.Git
		localRepoName    string
		inClusterRepoURL string
		gitrepoName      string
		r                = rand.New(rand.NewSource(GinkgoRandomSeed()))
		gitServerPort    int
		gitProtocol      string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		gitServerPort = 8080
		gitProtocol = "http"
		localRepoName = "repo"
	})

	JustBeforeEach(func() {
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host := githelper.BuildGitHostname()

		addr, err := githelper.GetExternalRepoAddr(env, gitServerPort, localRepoName)
		Expect(err).ToNot(HaveOccurred())
		addr = strings.Replace(addr, "http://", fmt.Sprintf("%s://", gitProtocol), 1)
		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, gitServerPort, localRepoName)

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpDir, localRepoName)

		gitrepoName = testenv.RandomFilename("helm-recreate-drift", r)
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir)

		_, _ = k.Delete("gitrepo", gitrepoName)
		_, _ = k.Delete("secret", "git-auth")
		_, _ = k.Delete("namespace", "helm-recreate-drift-test")

		// Check that the bundle deployment resource has been deleted
		Eventually(func(g Gomega) {
			out, _ := k.Get(
				"bundledeployments",
				"-A",
				"-l",
				fmt.Sprintf("fleet.cattle.io/repo-name=%s", gitrepoName),
			)
			g.Expect(out).To(ContainSubstring("No resources found"))
		}).Should(Succeed())
	})

	When("deploying a Helm chart with fields omitted by Helm v4", func() {
		JustBeforeEach(func() {
			// Create git auth secret
			_, err := k.Create(
				"secret", "generic", "git-auth", "--type", "kubernetes.io/basic-auth",
				"--from-literal=username=fleet-ci",
				"--from-literal=password=foo",
			)
			Expect(err).ToNot(HaveOccurred())

			err = testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/helm-recreate-drift-gitrepo.yaml"), struct {
				Name   string
				Repo   string
				Branch string
			}{
				Name:   gitrepoName,
				Repo:   inClusterRepoURL,
				Branch: gh.Branch,
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = gh.Create(clonedir, testenv.AssetPath("helm-recreate-drift"), "helm-recreate-drift")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not detect drift from Helm v4 rendering", func() {
			By("checking the bundle is ready")
			Eventually(func(g Gomega) {
				out, err := k.Get("bundles", "-A", "-l", fmt.Sprintf("fleet.cattle.io/repo-name=%s", gitrepoName))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(ContainSubstring("fleet-local"))
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())

			By("checking the bundle deployment is ready")
			var bundleDeploymentName, bdNamespace string
			Eventually(func(g Gomega) {
				out, err := k.Get("bundledeployments", "-A", "-o", "jsonpath={.items[0]['metadata.name','metadata.namespace']}", "-l", fmt.Sprintf("fleet.cattle.io/repo-name=%s", gitrepoName))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).ToNot(BeEmpty())

				data := strings.Split(out, " ")
				g.Expect(data).To(HaveLen(2))
				bundleDeploymentName, bdNamespace = data[0], data[1]
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				out, err := k.Namespace(bdNamespace).Get("bundledeployments", bundleDeploymentName, "-o", "jsonpath={.status.ready}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(Equal("true"))
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())

			By("checking the deployment has RollingUpdate strategy")
			Eventually(func(g Gomega) {
				out, err := k.Namespace("helm-recreate-drift-test").Get("deployments", "test-deployment", "-o", "jsonpath={.spec.strategy.type}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(Equal("RollingUpdate"))
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())

			By("verifying that no drift is detected")
			Eventually(func(g Gomega) {
				out, err := k.Namespace(bdNamespace).Get("bundledeployments", bundleDeploymentName, "-o", "jsonpath={.status.nonModified}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(Equal("true"), "BundleDeployment should not show drift from Helm v4 rendering")

				// Also check that modifiedStatus is empty
				out, err = k.Namespace(bdNamespace).Get("bundledeployments", bundleDeploymentName, "-o", "jsonpath={.status.modifiedStatus}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(BeEmpty(), "modifiedStatus should be empty when no drift is detected")
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())
		})
	})
})

package singlecluster_test

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("Monitoring Helm releases along GitRepo/bundle namespace updates", Ordered, func() {
	var (
		k            kubectl.Command
		r            = rand.New(rand.NewSource(GinkgoRandomSeed()))
		oldNamespace string
		newNamespace string
		gitrepoName  string
		path         string
	)

	BeforeAll(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		oldNamespace = testenv.NewNamespaceName("target", r)
		newNamespace = testenv.NewNamespaceName("target", r)
		gitrepoName = "releases-cleanup"
		path = "simple-chart"

		err := testenv.CreateGitRepo(
			k,
			oldNamespace,
			gitrepoName,
			"master",
			"",
			path,
		)
		Expect(err).ToNot(HaveOccurred())

		// Check for existence of Helm release as the only release in the target namespace
		checkRelease(oldNamespace, fmt.Sprintf("%s-%s", gitrepoName, path))
	})

	AfterAll(func() {
		out, err := k.Delete("gitrepo", gitrepoName)
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Delete("ns", oldNamespace, "--wait=false")
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Delete("ns", newNamespace, "--wait=false")
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("updating a bundle's namespace", func() {
		It("creates a new release in the new namespace", func() {
			Skip("This depends on a shorter garbage collection interval")
			out, err := k.Patch(
				"gitrepo",
				gitrepoName,
				"--type=merge",
				"-p",
				fmt.Sprintf(`{"spec":{"targetNamespace":"%s"}}`, newNamespace),
			)
			Expect(err).ToNot(HaveOccurred(), out)

			checkRelease(newNamespace, fmt.Sprintf("%s-%s", gitrepoName, path))
		})

		It("deletes the old release in the previous namespace", func() {
			Skip("This depends on a shorter garbage collection interval")
			Eventually(func(g Gomega) {
				cmd := exec.Command("helm", "list", "-q", "-n", oldNamespace)
				out, err := cmd.CombinedOutput()
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(string(out)).To(BeEmpty())
			}).Should(Succeed())
		})
	})
})

var _ = Describe("Monitoring Helm releases along GitRepo/bundle release name updates", Ordered, Label("infra-setup"), func() {
	var (
		tmpDir           string
		clonedir         string
		k                kubectl.Command
		gh               *githelper.Git
		clone            *git.Repository
		repoName         string
		inClusterRepoURL string
		gitrepoName      string
		r                = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace  string
		oldReleaseName   string
		newReleaseName   string
		assetPath        string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		oldReleaseName = "test-config-release"
		newReleaseName = "new-test-config-release"
		repoName = "repo"

		DeferCleanup(func() {
			out, err := k.Delete("gitrepo", gitrepoName)
			Expect(err).ToNot(HaveOccurred(), out)

			if targetNamespace != "" {
				out, err = k.Delete("ns", targetNamespace, "--wait=false")
				Expect(err).ToNot(HaveOccurred(), out)
			}

			_ = os.RemoveAll(tmpDir)
		})
	})

	JustBeforeEach(func() {
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host, err := githelper.BuildGitHostname(env.Namespace)
		Expect(err).ToNot(HaveOccurred())

		addr, err := githelper.GetExternalRepoAddr(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, port, repoName)

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpDir, repoName)

		gitrepoName = testenv.RandomFilename("releases-test", r)

		err = testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), struct {
			Name            string
			Repo            string
			Branch          string
			PollingInterval string
			TargetNamespace string
		}{
			gitrepoName,
			inClusterRepoURL,
			gh.Branch,
			"15s",           // default
			targetNamespace, // to avoid conflicts with other tests
		})
		Expect(err).ToNot(HaveOccurred())

		clone, err = gh.Create(clonedir, testenv.AssetPath(assetPath), "examples")
		Expect(err).ToNot(HaveOccurred())

		// Check for existence of Helm release with old name in the target namespace
		checkRelease(targetNamespace, oldReleaseName)
	})

	When("updating the release name of a bundle containing upstream Kubernetes resources", func() {
		BeforeEach(func() {
			assetPath = "helm/repo/with-release-name/no-crds"
			targetNamespace = testenv.NewNamespaceName("target", r)
		})
		It("replaces the release", func() {
			Skip("This depends on a shorter garbage collection interval")
			By("updating the release name in fleet.yaml")
			// Not adding `takeOwnership: true` leads to errors from Helm checking its own annotations and failing to
			// deploy the new release because the config map already exists and belongs to the old one.
			replace(
				path.Join(clonedir, "examples", "fleet.yaml"),
				oldReleaseName,
				fmt.Sprintf("%s\n  takeOwnership: true", newReleaseName),
			)

			_, err := gh.Update(clone)
			Expect(err).ToNot(HaveOccurred())

			By("creating a release with the new name")
			checkRelease(targetNamespace, newReleaseName)
		})
	})

	When("updating the release name of a bundle containing only CRDs", func() {
		BeforeEach(func() {
			assetPath = "helm/repo/with-release-name/crds-only"
			targetNamespace = ""
			DeferCleanup(func() {
				out, err := k.Delete("crd", "foobars.crd.test")
				Expect(err).ToNot(HaveOccurred(), out)
			})
		})
		It("replaces the release", func() {
			Skip("This depends on a shorter garbage collection interval")
			By("updating the release name in fleet.yaml")
			replace(
				path.Join(clonedir, "examples", "fleet.yaml"),
				oldReleaseName,
				fmt.Sprintf("%s\n  takeOwnership: true", newReleaseName),
			)

			_, err := gh.Update(clone)
			Expect(err).ToNot(HaveOccurred())

			By("creating a release with the new name")
			checkRelease("default", newReleaseName)
		})
	})
})

// checkRelease validates that namespace eventually contains a single release named releaseName.
func checkRelease(namespace, releaseName string) {
	var releases []string
	Eventually(func(g Gomega) {
		cmd := exec.Command("helm", "list", "-q", "-n", namespace)
		out, err := cmd.CombinedOutput()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(out)).ToNot(BeEmpty())

		releases = strings.Split(strings.TrimSpace(string(out)), "\n")

		g.Expect(len(releases)).To(Equal(1), strings.Join(releases, ","))
		g.Expect(releases[0]).To(Equal(releaseName))
	}).Should(Succeed())
}

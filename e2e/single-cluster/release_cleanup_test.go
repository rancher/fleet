package singlecluster_test

import (
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("Monitoring Helm releases along bundle namespace updates", Ordered, func() {
	var (
		k            kubectl.Command
		r            = rand.New(rand.NewSource(GinkgoRandomSeed()))
		oldNamespace string
		newNamespace string
		bundleName   string
	)

	type TemplateData struct {
		TargetNamespace string
	}

	BeforeAll(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		bundleName = "namespace-update"
		oldNamespace = testenv.NewNamespaceName("target", r)
		newNamespace = testenv.NewNamespaceName("target", r)

		GinkgoWriter.Printf("old namespace: %s\n", oldNamespace)
		GinkgoWriter.Printf("new namespace: %s\n", newNamespace)

		err := testenv.ApplyTemplate(
			k,
			testenv.AssetPath("single-cluster/release-cleanup/bundle-namespace-update.yaml"),
			TemplateData{TargetNamespace: oldNamespace},
		)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterAll(func() {
		out, err := k.Delete("bundle", bundleName)
		Expect(err).ToNot(HaveOccurred(), out)

		_, _ = k.Delete("ns", oldNamespace, "--wait=false")

		_, _ = k.Delete("ns", newNamespace, "--wait=false")
	})

	When("updating a bundle's namespace", func() {
		It("properly manages releases", func() {
			By("creating a new release in the new namespace")
			out, err := k.Patch(
				"bundle",
				bundleName,
				"--type=merge",
				"-p",
				fmt.Sprintf(`{"spec":{"namespace":"%s"}}`, newNamespace),
			)
			Expect(err).ToNot(HaveOccurred(), out)

			checkRelease(newNamespace, bundleName)

			By("deleting the old release in the previous namespace")
			Eventually(func(g Gomega) {
				cmd := exec.Command("helm", "list", "-q", "-n", oldNamespace)
				out, err := cmd.CombinedOutput()
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(string(out)).To(BeEmpty())
			}).Should(Succeed())
		})
	})
})

var _ = Describe("Monitoring Helm releases along bundle release name updates", Ordered, func() {
	var (
		k                kubectl.Command
		bundleName       string
		oldReleaseName   string
		newReleaseName   string
		assetPath        string
		releaseNamespace string
	)

	type TemplateData struct {
		ReleaseName string
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		oldReleaseName = "test-config-release"
		newReleaseName = "new-test-config-release"
		bundleName = "bundle-release-name-update"

		DeferCleanup(func() {
			out, err := k.Delete("bundle", bundleName)
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	JustBeforeEach(func() {
		err := testenv.ApplyTemplate(
			k,
			assetPath,
			TemplateData{ReleaseName: oldReleaseName},
		)
		Expect(err).ToNot(HaveOccurred())

		// Check for existence of Helm release with old name in the target namespace
		checkRelease(releaseNamespace, oldReleaseName)
	})

	When("updating the release name of a bundle containing upstream Kubernetes resources", func() {
		BeforeEach(func() {
			bundleName = "release-name-update"
			assetPath = testenv.AssetPath("single-cluster/release-cleanup/bundle-release-name-update.yaml")
			releaseNamespace = "default"
		})
		It("replaces the release", func() {
			By("updating the release name")
			// Not adding `takeOwnership: true` leads to errors from Helm checking its own annotations and failing to
			// deploy the new release because the config map already exists and belongs to the old one.
			out, err := k.Patch(
				"bundle",
				bundleName,
				"--type=merge",
				"-p",
				fmt.Sprintf(`{"spec":{"helm":{"releaseName":"%s", "takeOwnership": true}}}`, newReleaseName),
			)
			Expect(err).ToNot(HaveOccurred(), out)

			By("creating a release with the new name")
			checkRelease(releaseNamespace, newReleaseName)
		})
	})

	When("updating the release name of a bundle containing only CRDs", func() {
		BeforeEach(func() {
			bundleName = "release-name-update-crds"
			assetPath = testenv.AssetPath("single-cluster/release-cleanup/bundle-crds.yaml")
			releaseNamespace = "default"
			DeferCleanup(func() {
				out, err := k.Delete("crd", "foobars.crd.test")
				Expect(err).ToNot(HaveOccurred(), out)
			})
		})
		It("replaces the release", func() {
			By("updating the release name")
			out, err := k.Patch(
				"bundle",
				bundleName,
				"--type=merge",
				"-p",
				fmt.Sprintf(`{"spec":{"helm":{"releaseName":"%s", "takeOwnership": true}}}`, newReleaseName),
			)
			Expect(err).ToNot(HaveOccurred(), out)

			By("creating a release with the new name")
			checkRelease(releaseNamespace, newReleaseName)
		})
	})
})

// checkRelease validates that namespace eventually contains a single release named releaseName.
func checkRelease(namespace, releaseName string) {
	var releases []string
	Eventually(func(g Gomega) {
		cmd := exec.CommandContext(context.Background(), "helm", "list", "-q", "-n", namespace)
		out, err := cmd.CombinedOutput()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(out)).ToNot(BeEmpty())

		releases = strings.Split(strings.TrimSpace(string(out)), "\n")

		g.Expect(releases).To(HaveLen(1), strings.Join(releases, ","))
		g.Expect(releases[0]).To(Equal(releaseName))
	}).Should(Succeed())
}

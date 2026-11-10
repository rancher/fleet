package singlecluster_test

import (
	"math/rand"
	"os"
	"path"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("GitRepo bundle handover", Label("infra-setup"), func() {
	const (
		bundleName = "gitrepo-bundle-handover" // explicit name from fleet.yaml
		repoPath   = "app"
	)

	var (
		k               kubectl.Command
		r               = rand.New(rand.NewSource(GinkgoRandomSeed()))
		tmpDir          string
		oldName         string
		newName         string
		targetNamespace string
		gitrepoValues   gitRepoTestValues
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		oldName = testenv.RandomFilename("app-old", r)
		newName = testenv.RandomFilename("app-new", r)
		targetNamespace = testenv.NewNamespaceName("bundle-handover", r)

		addr, err := githelper.GetExternalRepoAddr(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		gh := githelper.NewHTTP(addr)
		gitrepoValues = gitRepoTestValues{
			Repo:            gh.GetInClusterURL(githelper.BuildGitHostname(), port, repoName),
			Branch:          gh.Branch,
			PollingInterval: "15s",
			TargetNamespace: targetNamespace,
			Path:            repoPath,
		}

		tmpDir, err = os.MkdirTemp("", "fleet-")
		Expect(err).ToNot(HaveOccurred())
		_, err = gh.Create(path.Join(tmpDir, repoName), testenv.AssetPath("gitrepo-bundle-handover/app"), repoPath)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			_ = os.RemoveAll(tmpDir)
			_, _ = k.Delete("gitrepo", oldName, "--ignore-not-found")
			_, _ = k.Delete("gitrepo", newName, "--ignore-not-found")
			Eventually(func() string {
				out, _ := k.Get("bundle", bundleName)
				return out
			}).Should(ContainSubstring("NotFound"))
			_, _ = k.Delete("ns", targetNamespace, "--wait=false")
		})
	})

	It("lets the new GitRepo inherit the bundle and its resources after the old one is deleted with keepResources", func() {
		By("deploying the bundle from the old GitRepo, without keepResources")
		applyHandoverGitRepo(k, gitrepoValues, oldName)
		Eventually(func() string { return getBundleOwner(k, bundleName) }).Should(Equal(oldName))
		Eventually(func() string { return getAppConfigUID(k, targetNamespace) }).ShouldNot(BeEmpty())
		uid := getAppConfigUID(k, targetNamespace)

		By("creating the new GitRepo, which is rejected because the old one owns the bundle")
		applyHandoverGitRepo(k, gitrepoValues, newName)
		Eventually(func() string { return getGitRepoStalledMessage(k, newName) }, testenv.MediumTimeout).Should(
			ContainSubstring(`owned by GitRepo "` + oldName + `"`))
		Expect(getBundleOwner(k, bundleName)).To(Equal(oldName))

		By("enabling keepResources on the old GitRepo")
		out, err := k.Patch("gitrepo", oldName, "--type=merge", "-p", `{"spec":{"keepResources":true}}`)
		Expect(err).ToNot(HaveOccurred(), out)
		Eventually(func() string {
			out, _ := k.Get("bundle", bundleName, "-o", "jsonpath={.spec.keepResources}")
			return out
		}).Should(Equal("true"))
		Eventually(func() string {
			out, _ := env.Kubectl.Get("bundledeployments", "-A",
				"-l", fleet.BundleLabel+"="+bundleName,
				"-o", "jsonpath={.items[*].spec.options.keepResources}")
			return out
		}).Should(Equal("true"))

		By("deleting the old GitRepo")
		out, err = k.Delete("gitrepo", oldName)
		Expect(err).ToNot(HaveOccurred(), out)
		Eventually(func() string {
			out, _ := k.Get("bundle", bundleName)
			return out
		}).Should(ContainSubstring("NotFound"))
		Eventually(func() string {
			out, _ := k.Namespace(targetNamespace).Get("secrets", "-l", "owner=helm")
			return out
		}).Should(ContainSubstring("No resources found"))
		Expect(getAppConfigUID(k, targetNamespace)).To(Equal(uid), "the configmap should be kept")

		By("force-syncing the new GitRepo")
		out, err = k.Patch("gitrepo", newName, "--type=merge", "-p", `{"spec":{"forceSyncGeneration":1}}`)
		Expect(err).ToNot(HaveOccurred(), out)

		By("checking the new GitRepo owns the bundle and deployed it")
		Eventually(func() string { return getBundleOwner(k, bundleName) }, testenv.MediumTimeout).Should(Equal(newName))
		Eventually(func() string {
			out, _ := k.Get("bundle", bundleName, "-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
			return out
		}, testenv.MediumTimeout).Should(Equal("True"))
		Eventually(func() string {
			out, _ := k.Namespace(targetNamespace).Get("secrets", "-l", "owner=helm", "-o", "jsonpath={.items[*].metadata.labels.name}")
			return out
		}).Should(Equal(bundleName))
		Eventually(func() string { return strings.TrimSpace(getGitRepoStalledMessage(k, newName)) }).Should(BeEmpty())

		By("checking the configmap was adopted, not recreated")
		Expect(getAppConfigUID(k, targetNamespace)).To(Equal(uid))
	})
})

// getBundleOwner returns the GitRepo named in the bundle's repo-name label, or
// an empty string if the bundle doesn't exist.
func getBundleOwner(k kubectl.Command, bundle string) string {
	out, err := k.Get("bundle", bundle, "-o", `jsonpath={.metadata.labels.fleet\.cattle\.io/repo-name}`)
	if err != nil {
		return ""
	}
	return out
}

// getAppConfigUID returns the UID of the app-config configmap deployed by the
// test bundle, or an empty string if it doesn't exist.
func getAppConfigUID(k kubectl.Command, namespace string) string {
	out, err := k.Namespace(namespace).Get("configmap", "app-config", "-o", "jsonpath={.metadata.uid}")
	if err != nil {
		return ""
	}
	return out
}

// getGitRepoStalledMessage returns the message of the GitRepo's Stalled condition.
func getGitRepoStalledMessage(k kubectl.Command, gitrepo string) string {
	out, err := k.Get("gitrepo", gitrepo, "-o", `jsonpath={.status.conditions[?(@.type=="Stalled")].message}`)
	Expect(err).ToNot(HaveOccurred(), out)
	return out
}

// applyHandoverGitRepo creates or updates a GitRepo with the given name from
// the shared values.
func applyHandoverGitRepo(k kubectl.Command, values gitRepoTestValues, name string) {
	values.Name = name
	err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), values)
	Expect(err).ToNot(HaveOccurred())
}

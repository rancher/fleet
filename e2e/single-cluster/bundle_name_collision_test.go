package singlecluster_test

import (
	"math/rand"
	"os"
	"path"
	"time"

	"github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("GitRepos using the same explicit bundle name", Label("infra-setup"), func() {
	const (
		bundleName    = "e2e-name-collision"
		configMapName = "name-collision"
	)

	var (
		tmpDir          string
		cloneDir        string
		clone           *git.Repository
		k               kubectl.Command
		gh              *githelper.Git
		repoURL         string
		ownerName       string
		otherName       string
		targetNamespace string
		r               = rand.New(rand.NewSource(GinkgoRandomSeed()))
	)

	applyGitRepo := func(name, clusterName string) {
		err := testenv.ApplyTemplate(k, testenv.AssetPath("name-collision/gitrepo.yaml"), struct {
			Name            string
			Repo            string
			Branch          string
			TargetNamespace string
			ClusterName     string
		}{name, repoURL, gh.Branch, targetNamespace, clusterName})
		Expect(err).ToNot(HaveOccurred())
	}

	stalledMessage := func(g Gomega, name string) string {
		for _, cond := range getGitRepoStatus(g, k, name).Conditions {
			if cond.Type == "Stalled" {
				return cond.Message
			}
		}
		return ""
	}

	collisionError := func() string {
		return `bundle "` + bundleName + `" in namespace "fleet-local" already belongs to GitRepo "` + ownerName +
			`" and cannot be overwritten by GitRepo "` + otherName + `"`
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		ownerName = testenv.RandomFilename("collision-owner", r)
		otherName = testenv.RandomFilename("collision-other", r)
		targetNamespace = testenv.NewNamespaceName("name-collision", r)

		addr, err := githelper.GetExternalRepoAddr(env, port, "repo")
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.NewHTTP(addr)
		repoURL = gh.GetInClusterURL(githelper.BuildGitHostname(), port, "repo")

		tmpDir, err = os.MkdirTemp("", "fleet-")
		Expect(err).ToNot(HaveOccurred())
		cloneDir = path.Join(tmpDir, "repo")
		clone, err = gh.Create(cloneDir, testenv.AssetPath("name-collision/bundle"), "examples")
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			_ = os.RemoveAll(tmpDir)
			_, _ = k.Delete("gitrepo", otherName)
			_, _ = k.Delete("gitrepo", ownerName)
			_, _ = k.Delete("ns", targetNamespace, "--wait=false")
		})
	})

	It("keeps the bundle and its deployment owned by the first GitRepo", func() {
		By("deploying the bundle from the owning GitRepo")
		applyGitRepo(ownerName, "local")
		Eventually(func(g Gomega) {
			out, err := k.Get("bundle", bundleName, `-o=jsonpath={.metadata.labels.fleet\.cattle\.io/repo-name}`)
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal(ownerName))

			out, err = k.Namespace(targetNamespace).Get("configmap", configMapName, "-o=jsonpath={.data.value}")
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal("initial"))
		}).Should(Succeed())

		By("rejecting the same bundle name from another GitRepo with different targets")
		applyGitRepo(otherName, "does-not-exist")
		Eventually(func(g Gomega) {
			g.Expect(stalledMessage(g, otherName)).To(ContainSubstring(collisionError()))
		}).Should(Succeed())

		By("keeping the existing bundle, its targets and its deployed resources")
		Consistently(func(g Gomega) {
			out, err := k.Get("bundle", bundleName, `-o=jsonpath={.metadata.labels.fleet\.cattle\.io/repo-name} {.spec.targets[*].clusterName}`)
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal(ownerName + " local"))

			out, err = k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/bundle-name="+bundleName, "-o=jsonpath={.items[*].metadata.name}")
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal(bundleName))

			out, err = k.Namespace(targetNamespace).Get("configmap", configMapName, "-o=jsonpath={.data.value}")
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal("initial"))
		}, 15*time.Second, time.Second).Should(Succeed())

		By("applying updates from the owning GitRepo")
		cm := path.Join(cloneDir, "examples", "configmap.yaml")
		Expect(os.WriteFile(cm, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+configMapName+"\ndata:\n  value: updated\n"), 0o600)).To(Succeed())
		_, err := gh.Update(clone)
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			out, err := k.Namespace(targetNamespace).Get("configmap", configMapName, "-o=jsonpath={.data.value}")
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal("updated"))
		}).Should(Succeed())
	})

	It("hands the bundle and its deployed resources over to another GitRepo after the owner is deleted with keepResources", func() {
		By("deploying the bundle from the owning GitRepo")
		applyGitRepo(ownerName, "local")
		var uid string
		Eventually(func(g Gomega) {
			out, err := k.Get("bundle", bundleName, `-o=jsonpath={.metadata.labels.fleet\.cattle\.io/repo-name}`)
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal(ownerName))

			uid, err = k.Namespace(targetNamespace).Get("configmap", configMapName, "-o=jsonpath={.metadata.uid}")
			g.Expect(err).ToNot(HaveOccurred(), uid)
			g.Expect(uid).ToNot(BeEmpty())
		}).Should(Succeed())

		By("rejecting the same bundle name from another GitRepo")
		applyGitRepo(otherName, "local")
		Eventually(func(g Gomega) {
			g.Expect(stalledMessage(g, otherName)).To(ContainSubstring(collisionError()))
		}).Should(Succeed())

		By("enabling keepResources on the owning GitRepo")
		out, err := k.Patch("gitrepo", ownerName, "--type=merge", "-p", `{"spec":{"keepResources":true}}`)
		Expect(err).ToNot(HaveOccurred(), out)
		Eventually(func(g Gomega) {
			out, err := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/bundle-name="+bundleName,
				"-o=jsonpath={.items[*].spec.options.keepResources}")
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal("true"))
		}).Should(Succeed())

		By("deleting the owning GitRepo")
		out, err = k.Delete("gitrepo", ownerName)
		Expect(err).ToNot(HaveOccurred(), out)
		Eventually(func(g Gomega) {
			out, _ := k.Get("bundle", bundleName)
			g.Expect(out).To(ContainSubstring("NotFound"))

			out, err := k.Namespace(targetNamespace).Get("secrets", "-l", "owner=helm", "-o=jsonpath={.items[*].metadata.name}")
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(BeEmpty())
		}).Should(Succeed())

		By("force-syncing the other GitRepo")
		out, err = k.Patch("gitrepo", otherName, "--type=merge", "-p", `{"spec":{"forceSyncGeneration":1}}`)
		Expect(err).ToNot(HaveOccurred(), out)

		By("adopting the bundle and keeping the deployed resources")
		Eventually(func(g Gomega) {
			out, err := k.Get("bundle", bundleName, `-o=jsonpath={.metadata.labels.fleet\.cattle\.io/repo-name}`)
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).To(Equal(otherName))

			out, err = k.Namespace(targetNamespace).Get("secrets", "-l", "owner=helm", "-o=jsonpath={.items[*].metadata.name}")
			g.Expect(err).ToNot(HaveOccurred(), out)
			g.Expect(out).ToNot(BeEmpty())

			g.Expect(stalledMessage(g, otherName)).To(BeEmpty())
		}).Should(Succeed())

		out, err = k.Namespace(targetNamespace).Get("configmap", configMapName, "-o=jsonpath={.metadata.uid}")
		Expect(err).ToNot(HaveOccurred(), out)
		Expect(out).To(Equal(uid), "expected the configmap to be adopted, not recreated")
	})
})

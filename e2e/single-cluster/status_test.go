package singlecluster_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Checks status updates happen for a simple deployment", Ordered, func() {
	var (
		k               kubectl.Command
		targetNamespace string
		deleteNamespace bool
	)

	type TemplateData struct {
		TargetNamespace string
		DeleteNamespace bool
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		deleteNamespace = false
	})

	JustBeforeEach(func() {
		err := testenv.ApplyTemplate(k, testenv.AssetPath("single-cluster/delete-namespace/gitrepo.yaml"),
			TemplateData{targetNamespace, deleteNamespace})

		Expect(err).ToNot(HaveOccurred())
		Eventually(func() error {
			out, err := k.Namespace(targetNamespace).Get("configmaps")
			if err != nil {
				return err
			}

			if !strings.Contains(out, "app-config") {
				return errors.New("expected configmap is not found")
			}

			return nil
		}).ShouldNot(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = k.Delete("gitrepo", "my-gitrepo")
		_, _ = k.Delete("ns", "my-custom-namespace", "--wait=false")
	})

	When("deployment is successful", func() {
		BeforeEach(func() {
			targetNamespace = "my-custom-namespace"
		})

		It("correctly sets status values", func() {
			By("correctly updating status values for GitRepos")
			Eventually(func(g Gomega) {
				out, err := k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.summary}'")
				g.Expect(err).ToNot(HaveOccurred(), out)

				g.Expect(out).Should(ContainSubstring("\"desiredReady\":1"))
				g.Expect(out).Should(ContainSubstring("\"ready\":1"))

				out, err = k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.display}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(ContainSubstring("\"readyBundleDeployments\":\"1/1\""))
			}).Should(Succeed())

			By("correctly updating status values for Clusters")
			Eventually(func(g Gomega) {
				out, err := k.Get("cluster", "local", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyBundles}'")
				g.Expect(err).ToNot(HaveOccurred(), out)

				// Expected 2 bundles instead of just 1 because fleet-agent is also included here
				g.Expect(out).Should(Equal("'2/2'"))
			}).Should(Succeed())

			By("correctly updating status values for ClusterGroups")
			Eventually(func(g Gomega) {
				out, err := k.Get("clustergroup", "default", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyBundles}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(Equal("'2/2'"))

				out, err = k.Get("clustergroup", "default", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyClusters}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(Equal("'1/1'"))
			}).Should(Succeed())

			By("correctly updating status values for bundles")
			Eventually(func(g Gomega) {
				out, err := k.Get("bundle", "my-gitrepo-helm-verify", "-n", "fleet-local", "-o", "jsonpath='{.status.summary}'")
				g.Expect(err).ToNot(HaveOccurred(), out)

				g.Expect(out).Should(ContainSubstring("\"desiredReady\":1"))
				g.Expect(out).Should(ContainSubstring("\"ready\":1"))

				out, err = k.Get("bundle", "my-gitrepo-helm-verify", "-n", "fleet-local", "-o", "jsonpath='{.status.display}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(ContainSubstring("\"readyClusters\":\"1/1\""))
			}).Should(Succeed())

		})
	})
})

var _ = Describe("Checks that template errors are shown in bundles and gitrepos", Ordered, Label("infra-setup"), func() {
	var (
		tmpDir           string
		cloneDir         string
		k                kubectl.Command
		gh               *githelper.Git
		repoName         string
		inClusterRepoURL string
		gitrepoName      string
		r                = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace  string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		repoName = "repo"
	})

	JustBeforeEach(func() {
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host := githelper.BuildGitHostname()

		addr, err := githelper.GetExternalRepoAddr(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, port, repoName)

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		cloneDir = path.Join(tmpDir, repoName)

		gitrepoName = testenv.RandomFilename("status-test", r)

		_, err = gh.Create(cloneDir, testenv.AssetPath("status/chart-with-template-vars"), "examples")
		Expect(err).ToNot(HaveOccurred())

		err = testenv.ApplyTemplate(k, testenv.AssetPath("status/gitrepo.yaml"), struct {
			Name            string
			Repo            string
			Branch          string
			TargetNamespace string
		}{
			gitrepoName,
			inClusterRepoURL,
			gh.Branch,
			targetNamespace, // to avoid conflicts with other tests
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir)

		_, err := k.Delete("gitrepo", gitrepoName)
		Expect(err).ToNot(HaveOccurred())

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

		// Deleting the targetNamespace is not necessary when the GitRepo did not successfully
		// render, as in a few test cases here. If no targetNamespace was created, trying to delete
		// the namespace will result in an error, which is why we are not checking for errors when
		// deleting namespaces here.
		_, _ = k.Delete("ns", targetNamespace)
	})

	expectNoError := func(g Gomega, conditions []genericcondition.GenericCondition) {
		for _, condition := range conditions {
			if condition.Type == string(fleet.Ready) {
				g.Expect(condition.Status).To(Equal(corev1.ConditionTrue))
				g.Expect(condition.Message).To(BeEmpty())
				break
			}
		}
	}

	expectTargetingError := func(g Gomega, conditions []genericcondition.GenericCondition) {
		found := false
		for _, condition := range conditions {
			if condition.Type == string(fleet.Ready) {
				g.Expect(condition.Status).To(Equal(corev1.ConditionFalse))
				g.Expect(condition.Message).To(ContainSubstring("targeting error"))
				g.Expect(condition.Message).To(
					ContainSubstring(
						"<.ClusterLabels.foo>: map has no entry for key \"foo\""))
				found = true
				break
			}
		}
		g.Expect(found).To(BeTrue())
	}

	ensureClusterHasLabelFoo := func() (string, error) {
		return k.Namespace("fleet-local").
			Patch("cluster", "local", "--type", "json", "--patch",
				`[{"op": "add", "path": "/metadata/labels/foo", "value": "bar"}]`)
	}

	ensureClusterHasNoLabelFoo := func() (string, error) {
		return k.Namespace("fleet-local").
			Patch("cluster", "local", "--type", "json", "--patch",
				`[{"op": "remove", "path": "/metadata/labels/foo"}]`)
	}

	When("a git repository is created that contains a template error", func() {
		BeforeEach(func() {
			targetNamespace = testenv.NewNamespaceName("target", r)
		})

		It("should exhibit the error", func() {
			By("showing the error in the bundle")
			_, _ = ensureClusterHasNoLabelFoo()
			Eventually(func(g Gomega) {
				status := getBundleStatus(g, k, gitrepoName+"-examples")
				expectTargetingError(g, status.Conditions)
			}).Should(Succeed())

			By("showing the error in the gitrepo")
			_, _ = ensureClusterHasNoLabelFoo()
			Eventually(func(g Gomega) {
				status := getGitRepoStatus(g, k, gitrepoName)
				expectTargetingError(g, status.Conditions)
			}).Should(Succeed())
		})
	})

	When("a git repository is created that contains no template error", func() {
		It("should have no error in the bundle", func() {
			_, _ = ensureClusterHasLabelFoo()
			Eventually(func(g Gomega) {
				status := getBundleStatus(g, k, gitrepoName+"-examples")
				expectNoError(g, status.Conditions)
			}).Should(Succeed())
		})
	})
})

// Checks that once a cluster goes offline after a failed deployment, the bundle
// status does not permanently show the stale error after a fix commit is applied
// (issue https://github.com/rancher/fleet/issues/594).
var _ = Describe("Bundle status does not retain stale error for offline cluster after fix", Ordered, Label("infra-setup"), func() {
	const (
		localAgentNS     = "cattle-fleet-local-system"
		fleetAgentDeploy = "fleet-agent"
	)

	var (
		tmpDir           string
		cloneDir         string
		k                kubectl.Command
		kAgent           kubectl.Command
		gh               *githelper.Git
		inClusterRepoURL string
		gitrepoName      string
		clone            *gogit.Repository
		targetNamespace  string
		r                = rand.New(rand.NewSource(GinkgoRandomSeed()))
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		kAgent = env.Kubectl.Namespace(localAgentNS)
	})

	JustBeforeEach(func() {
		host := githelper.BuildGitHostname()
		addr, err := githelper.GetExternalRepoAddr(env, port, "repo")
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, port, "repo")

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		cloneDir = path.Join(tmpDir, "repo")

		gitrepoName = testenv.RandomFilename("offline-stuck", r)
		targetNamespace = testenv.NewNamespaceName("offline-stuck", r)

		clone, err = gh.Create(cloneDir, testenv.AssetPath("single-cluster/offline-bundle-stuck"), "examples")
		Expect(err).ToNot(HaveOccurred())

		err = testenv.ApplyTemplate(k, testenv.AssetPath("status/gitrepo.yaml"), struct {
			Name            string
			Repo            string
			Branch          string
			TargetNamespace string
		}{
			gitrepoName,
			inClusterRepoURL,
			gh.Branch,
			targetNamespace,
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterAll(func() {
		// Ensure fleet-agent is always restored, even when the test fails midway.
		_, _ = kAgent.Run("scale", "deployment", fleetAgentDeploy, "--replicas=1", "--timeout=60s")

		_ = os.RemoveAll(tmpDir)
		_, _ = k.Delete("gitrepo", gitrepoName)

		Eventually(func(g Gomega) {
			out, _ := k.Get(
				"bundledeployments",
				"-A",
				"-l",
				fmt.Sprintf("fleet.cattle.io/repo-name=%s", gitrepoName),
			)
			g.Expect(out).To(ContainSubstring("No resources found"))
		}).Should(Succeed())

		_, _ = k.Delete("ns", targetNamespace, "--wait=false")
	})

	It("clears the stale error from an offline cluster once a fix commit is present", func() {
		bundleName := gitrepoName + "-examples"

		By("waiting for the initial deployment to be Ready")
		Eventually(func(g Gomega) {
			status := getBundleStatus(g, k, bundleName)
			g.Expect(status.Summary.Ready).To(Equal(1))
		}).Should(Succeed())

		By("pushing a commit that introduces a YAML parse error")
		badContent := `apiVersion: v1
kind: ConfigMap
metadata:
  name: offline-bundle-stuck-cm
data:
  broken: {unclosed
`
		err := os.WriteFile(path.Join(cloneDir, "examples", "templates", "configmap.yaml"), []byte(badContent), 0644)
		Expect(err).ToNot(HaveOccurred())
		_, err = gh.Update(clone)
		Expect(err).ToNot(HaveOccurred())

		By("waiting for the bundle to reflect the YAML parse error")
		Eventually(func(g Gomega) {
			status := getBundleStatus(g, k, bundleName)
			g.Expect(status.Summary.Ready).To(Equal(0))
			found := false
			for _, cond := range status.Conditions {
				if cond.Type == string(fleet.Ready) && strings.Contains(cond.Message, "did not find expected") {
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "expected YAML parse error in bundle conditions, got: %v", status.Conditions)
		}, testenv.MediumTimeout, testenv.PollingInterval).Should(Succeed())

		By("scaling down the fleet-agent to simulate an offline cluster")
		out, err := kAgent.Run("scale", "deployment", fleetAgentDeploy, "--replicas=0", "--timeout=60s")
		Expect(err).ToNot(HaveOccurred(), out)

		// Wait until the agent pod is gone so it cannot apply any further commits.
		Eventually(func(g Gomega) {
			out, _ := kAgent.Get("pods", "-l", "app=fleet-agent")
			g.Expect(out).To(ContainSubstring("No resources found"))
		}).Should(Succeed())

		By("pushing an intermediate commit that does not fix the YAML error")
		intermediateContent := `apiVersion: v1
kind: ConfigMap
metadata:
  name: offline-bundle-stuck-cm
  labels:
    version: "2"
data:
  broken: {unclosed
`
		err = os.WriteFile(path.Join(cloneDir, "examples", "templates", "configmap.yaml"), []byte(intermediateContent), 0644)
		Expect(err).ToNot(HaveOccurred())
		_, err = gh.Update(clone)
		Expect(err).ToNot(HaveOccurred())

		By("pushing a fix commit (valid YAML)")
		fixContent := `apiVersion: v1
kind: ConfigMap
metadata:
  name: offline-bundle-stuck-cm
data:
  key: fixed
`
		err = os.WriteFile(path.Join(cloneDir, "examples", "templates", "configmap.yaml"), []byte(fixContent), 0644)
		Expect(err).ToNot(HaveOccurred())
		_, err = gh.Update(clone)
		Expect(err).ToNot(HaveOccurred())

		By("verifying the error message does not persist after the fix commit is pushed")
		// After the controller picks up the fix commit it updates the BD spec.
		// Even though the offline agent cannot apply the fix yet, the bundle
		// should no longer surface the stale error from the previous apply attempt.
		Eventually(func(g Gomega) {
			status := getBundleStatus(g, k, bundleName)
			found := false
			for _, cond := range status.Conditions {
				if cond.Type == string(fleet.Ready) {
					found = true
					g.Expect(cond.Message).NotTo(
						ContainSubstring("did not find expected"),
						"bundle Ready condition still shows stale YAML error after fix commit was pushed",
					)
					break
				}
			}
			g.Expect(found).To(BeTrue(), "expected Ready condition to be present, got: %v", status.Conditions)
		}, testenv.LongTimeout, testenv.PollingInterval).Should(Succeed())

		By("scaling the fleet-agent back up")
		out, err = kAgent.Run("scale", "deployment", fleetAgentDeploy, "--replicas=1", "--timeout=60s")
		Expect(err).ToNot(HaveOccurred(), out)

		By("waiting for the bundle to become Ready after the agent recovers")
		Eventually(func(g Gomega) {
			status := getBundleStatus(g, k, bundleName)
			g.Expect(status.Summary.Ready).To(Equal(1))
		}, testenv.LongTimeout, testenv.PollingInterval).Should(Succeed())
	})
})

// getBundleStatus retrieves the status of the bundle with the provided name.
func getBundleStatus(g Gomega, k kubectl.Command, name string) fleet.BundleStatus {
	gr, err := k.Get("bundle", name, "-o=json")

	g.Expect(err).ToNot(HaveOccurred())

	var bundle fleet.Bundle
	_ = json.Unmarshal([]byte(gr), &bundle)

	return bundle.Status
}

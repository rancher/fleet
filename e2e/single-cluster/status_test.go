package singlecluster_test

import (
	"encoding/json"
	"errors"
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

		It("correctly sets the status values for GitRepos", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.summary}'")
				g.Expect(err).ToNot(HaveOccurred(), out)

				g.Expect(out).Should(ContainSubstring("\"desiredReady\":1"))
				g.Expect(out).Should(ContainSubstring("\"ready\":1"))

				out, err = k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.display}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(ContainSubstring("\"readyBundleDeployments\":\"1/1\""))
			}).Should(Succeed())
		})

		It("correctly sets the status values for Clusters", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("cluster", "local", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyBundles}'")
				g.Expect(err).ToNot(HaveOccurred(), out)

				// Expected 2 bundles instead of just 1 because fleet-agent is also included here
				g.Expect(out).Should(Equal("'2/2'"))
			}).Should(Succeed())
		})

		It("correctly sets the status values for ClusterGroups", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("clustergroup", "default", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyBundles}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(Equal("'2/2'"))

				out, err = k.Get("clustergroup", "default", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyClusters}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(Equal("'1/1'"))
			}).Should(Succeed())
		})

		It("correctly sets the status values for bundle", func() {
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
		host, err := githelper.BuildGitHostname(env.Namespace)
		Expect(err).ToNot(HaveOccurred())

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
				g.Expect(condition.Message).To(ContainSubstring("Targeting error"))
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

		It("should have an error in the bundle", func() {
			_, _ = ensureClusterHasNoLabelFoo()
			Eventually(func(g Gomega) {
				status := getBundleStatus(g, k, gitrepoName+"-examples")
				expectTargetingError(g, status.Conditions)
			}).Should(Succeed())
		})

		It("should have an error in the gitrepo", func() {
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

// getBundleStatus retrieves the status of the bundle with the provided name.
func getBundleStatus(g Gomega, k kubectl.Command, name string) fleet.BundleStatus {
	gr, err := k.Get("bundle", name, "-o=json")

	g.Expect(err).ToNot(HaveOccurred())

	var bundle fleet.Bundle
	_ = json.Unmarshal([]byte(gr), &bundle)

	return bundle.Status
}

package singlecluster_test

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
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

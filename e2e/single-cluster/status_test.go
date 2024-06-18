package singlecluster_test

import (
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("Checks status updates happen for a simple deployment", func() {
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

		DeferCleanup(func() {
			_, _ = k.Delete("ns", "my-custom-namespace", "--wait=false")
		})
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

	When("deployment is successful", func() {
		BeforeEach(func() {
			targetNamespace = "my-custom-namespace"
		})

		It("correctly sets the status values for GitRepos", func() {
			out, err := k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.summary}'")
			Expect(err).ToNot(HaveOccurred(), out)

			Expect(out).Should(ContainSubstring("\"desiredReady\":1"))
			Expect(out).Should(ContainSubstring("\"ready\":1"))

			out, err = k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.display}'")
			Expect(err).ToNot(HaveOccurred(), out)
			Expect(out).Should(ContainSubstring("\"readyBundleDeployments\":\"1/1\""))
		})

		It("correctly sets the status values for bundle", func() {
			out, err := k.Get("bundle", "my-gitrepo-helm-verify", "-n", "fleet-local", "-o", "jsonpath='{.status.summary}'")
			Expect(err).ToNot(HaveOccurred(), out)

			Expect(out).Should(ContainSubstring("\"desiredReady\":1"))
			Expect(out).Should(ContainSubstring("\"ready\":1"))

			out, err = k.Get("bundle", "my-gitrepo-helm-verify", "-n", "fleet-local", "-o", "jsonpath='{.status.display}'")
			Expect(err).ToNot(HaveOccurred(), out)
			Expect(out).Should(ContainSubstring("\"readyClusters\":\"1/1\""))
		})
	})

	When("bundle is deleted", func() {
		BeforeEach(func() {
			targetNamespace = "my-custom-namespace"
		})

		It("correctly updates the status fields for GitRepos", func() {
			out, err := k.Delete("bundle", "my-gitrepo-helm-verify", "-n", "fleet-local")
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func() error {
				out, err = k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.summary}'")
				if err != nil {
					return err
				}

				expectedDesiredReady := "\"desiredReady\":0"
				if !strings.Contains(out, expectedDesiredReady) {
					return fmt.Errorf("expected %q not found in %q", expectedDesiredReady, out)
				}

				expectedReady := "\"ready\":0"
				if !strings.Contains(out, expectedReady) {
					return fmt.Errorf("expected %q not found in %q", expectedReady, out)
				}

				out, err = k.Get(
					"gitrepo",
					"my-gitrepo",
					"-n",
					"fleet-local",
					"-o",
					"jsonpath='{.status.display}'",
				)
				if err != nil {
					return err
				}

				expectedReadyBD := "\"readyBundleDeployments\":\"0/0\""
				if !strings.Contains(out, expectedReadyBD) {
					return fmt.Errorf("expected %q not found in %q", expectedReadyBD, out)
				}

				return nil
			}).ShouldNot(HaveOccurred())
		})
	})
})

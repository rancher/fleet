package singlecluster_test

import (
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("delete namespaces", func() {
	var (
		k               kubectl.Command
		targetNamespace string
		deleteNamespace bool
		interval        = 100 * time.Millisecond
		duration        = 2 * time.Second
	)

	type TemplateData struct {
		TargetNamespace string
		DeleteNamespace bool
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		deleteNamespace = false

		DeferCleanup(func() {
			_, _ = k.Delete("ns", "my-custom-namespace")
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

	When("delete namespaces is false", func() {
		BeforeEach(func() {
			targetNamespace = "my-custom-namespace"
		})

		It("preserves targetNamespace when GitRepo is deleted", func() {
			out, err := k.Delete("gitrepo", "my-gitrepo", "-n", "fleet-local")
			Expect(err).ToNot(HaveOccurred(), out)

			Consistently(func() error {
				_, err := k.Get("namespaces", targetNamespace)
				return err
			}, duration, interval).ShouldNot(HaveOccurred())
		})
	})

	When("delete namespaces is true", func() {
		BeforeEach(func() {
			deleteNamespace = true
			targetNamespace = "my-custom-namespace"
		})

		It("targetNamespace is deleted after deleting gitRepo", func() {
			_, err := k.Get("namespaces", targetNamespace)
			Expect(err).To(BeNil())

			out, err := k.Delete("gitrepo", "my-gitrepo", "-n", "fleet-local")
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func() error {
				_, err = k.Get("namespaces", targetNamespace)
				return err
			}).ShouldNot(BeNil())
		})
	})

	When("delete namespaces is true but resources are deployed in default namespace", func() {
		BeforeEach(func() {
			deleteNamespace = true
			targetNamespace = "default"
		})

		It("default namespace exists", func() {
			out, err := k.Delete("gitrepo", "my-gitrepo", "-n", "fleet-local")
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func() string {
				out, _ = k.Namespace(targetNamespace).Get("configmap", "app-config", "-o", "yaml")
				return out
			}).Should(ContainSubstring("Error from server (NotFound)"))

			_, err = k.Get("namespaces", targetNamespace)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

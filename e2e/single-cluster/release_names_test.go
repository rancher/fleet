package singlecluster_test

import (
	"strings"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Release Name", func() {
	var (
		asset string
		k     kubectl.Command

		// namespaces from test data in single-cluster/release-names.yaml
		namespaces = []string{
			"workloadns1",
			"workloadns2",
			"workloadns4",
			"workloadns5",
			"workloadns6",
			"workloadns7",
			"workloadns8",
			"workloadns9",
			"workloadns10",
		}
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {
		out, err := k.Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)

		DeferCleanup(func() {
			out, err := k.Delete("-f", testenv.AssetPath(asset))
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	Context("complicated bundle names", func() {
		BeforeEach(func() {
			asset = "single-cluster/release-names.yaml"
		})

		It("deploys all workloads with valid names", func() {
			Eventually(func() []string {
				out, _ := k.Get("bundles", "-A", "-o=jsonpath={.items[*].metadata.name}")

				return strings.Split(out, " ")
			}).Should(ContainElements(
				"long-name-test-customhelmreleasename",
				"long-name-test-customspecialhelmreleasename",
				"long-name-test-shortpath",
				ContainSubstring("long-name-test-shortpath-with-char-"),
				ContainSubstring("long-name-test-longpathwithmorecharactersthanyo-"),
				ContainSubstring("long-name-test-funcharts-0-app-"),
				ContainSubstring("long-name-test-funcharts-app-12-factor-"),
			))

			for _, ns := range namespaces {
				Eventually(func() string {
					out, _ := k.Namespace(ns).Get("configmaps")

					return out
				}).Should(ContainSubstring("app-config"))
			}

			By("making sure helm release names match the bundle name", func() {
				for _, ns := range []string{
					"workloadns4",
					"workloadns5",
					"workloadns6",
					"workloadns7",
					"workloadns8",
					"workloadns9",
					"workloadns10",
				} {
					name, _ := k.Get("bundles", "-A", `-o=go-template={{- range .items -}}{{- if eq .spec.namespace "`+ns+`" }}{{ .metadata.name }}{{ end }}{{ end }}`)

					// it should only contain the correct release name
					out, _ := k.Get("secrets", "-n", ns, "-o=jsonpath={.items[*].metadata.labels.name}")
					releases := strings.Split(out, " ")
					Expect(releases).To(HaveEach(name))
				}
			})
		})
	})
})

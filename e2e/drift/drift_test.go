package examples_test

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Drift", Ordered, func() {
	var (
		asset      string
		k          kubectl.Command
		namespace  string
		bundleName string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		namespace = "drift"
	})

	JustBeforeEach(func() {
		out, err := k.Namespace(env.Namespace).Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)

		var bundle fleet.Bundle
		Eventually(func() int {
			bundle = getBundle(bundleName, k)
			return bundle.Status.Summary.Ready
		}).Should(Equal(1), fmt.Sprintf("Summary: %+v", bundle.Status.Summary))

		defer func() {
			if r := recover(); r != nil {
				bundle := getBundle(bundleName, k)
				GinkgoWriter.Printf("bundle status: %v", bundle.Status)
			}
		}()
	})

	AfterEach(func() {
		out, err := k.Namespace(env.Namespace).Delete("-f", testenv.AssetPath(asset), "--wait")
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterAll(func() {
		_, _ = k.Delete("ns", namespace)
		_, _ = k.Delete("ns", "drift-ignore-status")
	})

	When("Drift correction is enabled without force", func() {
		BeforeEach(func() {
			asset = "drift/correction-enabled/gitrepo.yaml"
			bundleName = "drift-correction-test-drift"
		})

		// Helm rollback uses three-way merge by default (without force), which fails when trying to rollback a change made on an item in the ports array.
		Context("Modifying port in service", func() {
			JustBeforeEach(func() {
				kw := k.Namespace(namespace)
				out, err := kw.Patch(
					"service", "drift-dummy-service",
					"-o=json",
					"--type=json",
					"-p", `[{"op": "replace", "path": "/spec/ports/0/port", "value": 1234}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
				GinkgoWriter.Print(out)
			})

			// Note: more accurate checks on status changes are now done in integration tests.
			It("Corrects drift when drift correction is set to force", func() {
				Eventually(func() string {
					out, _ := k.Namespace(env.Namespace).Get("bundles", bundleName, "-o=jsonpath={.status.conditions[*].message}")
					return out
				}).Should(ContainSubstring(`service.v1 drift/drift-dummy-service modified`))

				out, err := k.Patch(
					"gitrepo",
					"drift-correction-test",
					"--type=merge",
					"-p",
					`{"spec":{"correctDrift":{"force": true}}}`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
				GinkgoWriter.Print(out)
				Eventually(func() string {
					out, _ := k.Namespace(env.Namespace).Get("bundles", bundleName, "-o=jsonpath={.status.conditions[*].message}")
					return out
				}).ShouldNot(ContainSubstring(`drift-dummy-service modified`))
			})
		})
	})
})

func getBundle(bundleName string, k kubectl.Command) fleet.Bundle {
	out, _ := k.Namespace(env.Namespace).Get("bundles", bundleName, "-o=json")
	var bundle fleet.Bundle
	_ = json.Unmarshal([]byte(out), &bundle)

	return bundle
}

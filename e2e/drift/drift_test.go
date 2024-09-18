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
})

func getBundle(bundleName string, k kubectl.Command) fleet.Bundle {
	out, _ := k.Namespace(env.Namespace).Get("bundles", bundleName, "-o=json")
	var bundle fleet.Bundle
	_ = json.Unmarshal([]byte(out), &bundle)

	return bundle
}

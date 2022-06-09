package bundle_test

import (
	"context"
	"os"
	"path"

	"github.com/rancher/fleet/pkg/bundle"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Bundle", func() {
	Describe("Read", func() {
		It("loads a fleet.yaml", func() {
			b, _ := bundle.Open(context.TODO(), "test", examplePath("multi-cluster", "helm"), "", nil)
			Expect(b).ToNot(BeNil())
			Expect(b.Definition.Spec.BundleDeploymentOptions.TargetNamespace).To(Equal("fleet-mc-helm-example"))
			Expect(b.Definition.Spec.Resources).To(HaveLen(10))
			Expect(b.Definition.Spec.Targets).To(ContainElement(HaveField("Name", Equal("test"))))
		})
	})

	Describe("Match", func() {
		var (
			b   *bundle.Bundle
			dir string
		)

		BeforeEach(func() {
			dir = examplePath("multi-cluster", "helm")
		})

		JustBeforeEach(func() {
			r, err := os.Open(path.Join(dir, "fleet.yaml"))
			Expect(err).ToNot(HaveOccurred())
			b, err = bundle.Read(context.TODO(), "test", dir, r, nil)
			Expect(err).ToNot(HaveOccurred())
		})

		It("matches a cluster by label", func() {
			labels := map[string]string{"env": "dev"}
			match := b.Match("", map[string]map[string]string{
				"default": labels,
			}, labels)
			Expect(match).ToNot(BeNil())
			Expect(match.Bundle).To(Equal(b))
			Expect(match.Target.Name).To(Equal("dev"))
			Expect(match.Target.ClusterSelector.MatchLabels).To(HaveKeyWithValue("env", "dev"))
		})
	})
})

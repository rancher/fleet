package integration_test

import (
	"context"
	"os"

	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"gopkg.in/yaml.v2"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("Apply", func() {
	var (
		c          *client.Getter
		currentDir string
		dir        string
	)

	AfterEach(func() {
		err := os.Chdir(currentDir)
		Expect(err).ToNot(HaveOccurred())
	})

	JustBeforeEach(func() {
		c = client.NewGetter("", "", "fleet-local")

		// apply() behaves different if in the same dir, so chdir first
		currentDir, _ = os.Getwd()
		err := os.Chdir(dir)
		Expect(err).ToNot(HaveOccurred())
	})

	When("given an existing directory", func() {
		BeforeEach(func() {
			dir = examplePath("single-cluster", "helm")
		})

		It("creates a bundle and adds the label", func() {
			buf := gbytes.NewBuffer()
			opts := &apply.Options{
				// write to buffer, instead of cluster
				Output: buf,
				// add label to bundles
				Labels: map[string]string{"fleet.cattle.io/commit": "fake"},
			}

			name := "test"
			baseDirs := []string{}
			err := apply.Apply(context.Background(), c, name, baseDirs, opts)
			Expect(err).ToNot(HaveOccurred())
			Expect(buf).To(gbytes.Say(`apiVersion: fleet.cattle.io/v1alpha1
kind: Bundle
metadata:
  labels:
    fleet.cattle.io/commit: fake
  name: test
  namespace: fleet-local
`))
			Expect(buf).To(gbytes.Say("app: redis"))
		})
	})

	When("applying a multi-cluster example", func() {
		BeforeEach(func() {
			dir = examplePath("multi-cluster", "kustomize")
		})

		It("creates a bundle with all the resources", func() {
			buf := gbytes.NewBuffer()
			opts := &apply.Options{Output: buf}

			name := "test"
			baseDirs := []string{}
			err := apply.Apply(context.Background(), c, name, baseDirs, opts)
			Expect(err).ToNot(HaveOccurred())

			b := &fleet.Bundle{}
			err = yaml.Unmarshal(buf.Contents(), b)
			Expect(err).ToNot(HaveOccurred())

			Expect(b.Name).To(BeEmpty())
			Expect(b.Spec.Resources).To(HaveLen(17))
			Expect(b.Spec.Targets).To(HaveLen(3))
		})
	})
})

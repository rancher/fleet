package bundle_test

import (
	"context"
	"os"

	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("Apply", func() {
	currentDir, _ := os.Getwd()

	When("Apply", func() {
		AfterEach(func() {
			err := os.Chdir(currentDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates a bundle", func() {
			client := client.NewGetter("", "", "fleet-local")
			// behaves different if in the same dir
			name := "test"
			err := os.Chdir(examplePath("single-cluster/helm"))
			baseDirs := []string{}
			Expect(err).ToNot(HaveOccurred())

			// write to buffer, instead of cluster
			buf := gbytes.NewBuffer()
			opts := &apply.Options{
				Output: buf,
				Labels: map[string]string{"fleet.cattle.io/commit": "fake"},
			}

			err = apply.Apply(context.Background(), client, name, baseDirs, opts)
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
})

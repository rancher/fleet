package singlecluster_test

import (
	"encoding/json"
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

// Fleet server-side applies the namespace labels and annotations from the
// bundle options with a dedicated field manager, so it owns exactly the keys it
// declares: a key it stops declaring is pruned, and keys held by any other
// field manager are left alone.
//
// The previous read-modify-write update deleted every key that was not listed
// in the options, no matter who wrote it, which wiped metadata owned by others
// (Rancher's field.cattle.io/projectId, for example). See issue #4564.
var _ = Describe("Namespace label and annotation ownership", func() {
	const (
		bundleName = "namespace-metadata"

		// Written straight to the namespace with kubectl, so the API server
		// records kubectl as the owning field manager. Fleet must not touch it.
		foreignKey   = "e2e.fleet.cattle.io/owned-elsewhere"
		foreignValue = "survives"

		// Both start out in the bundle options; only prunedKey is removed from
		// them later in the spec.
		keptKey   = "fleet.e2e/keep"
		prunedKey = "fleet.e2e/prune"
	)

	var (
		k               kubectl.Command
		r               = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace string
	)

	type TemplateData struct {
		TargetNamespace string
	}

	// nsMetadata reads .metadata.<field> off the target namespace, where field
	// is "labels" or "annotations".
	nsMetadata := func(g Gomega, field string) map[string]string {
		out, err := k.Get("ns", targetNamespace, "-o", "jsonpath={.metadata."+field+"}")
		g.Expect(err).ToNot(HaveOccurred(), out)

		m := map[string]string{}
		g.Expect(json.Unmarshal([]byte(out), &m)).To(Succeed(), out)

		return m
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		targetNamespace = testenv.NewNamespaceName("ns-metadata", r)

		DeferCleanup(func() {
			out, err := k.Delete("bundle", bundleName)
			Expect(err).ToNot(HaveOccurred(), out)

			_, _ = k.Delete("ns", targetNamespace, "--wait=false")
		})

		Expect(testenv.ApplyTemplate(
			k,
			testenv.AssetPath("single-cluster/bundle-namespace-metadata.yaml"),
			TemplateData{TargetNamespace: targetNamespace},
		)).To(Succeed())
	})

	It("prunes only the keys it declares itself", func() {
		By("applying the bundle's namespaceLabels and namespaceAnnotations to the release namespace")
		Eventually(func(g Gomega) {
			g.Expect(nsMetadata(g, "labels")).To(HaveKeyWithValue(prunedKey, "two"))
			g.Expect(nsMetadata(g, "annotations")).To(HaveKeyWithValue(prunedKey, "two"))
		}).Should(Succeed())

		By("adding a label and an annotation owned by a different field manager")
		out, err := k.Label("ns", targetNamespace, foreignKey+"="+foreignValue)
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Run("annotate", "ns", targetNamespace, foreignKey+"="+foreignValue)
		Expect(err).ToNot(HaveOccurred(), out)

		By("dropping one key from the bundle's namespaceLabels and namespaceAnnotations")
		out, err = k.Patch(
			"bundle",
			bundleName,
			"--type=merge",
			"-p",
			`{"spec":{"namespaceLabels":{"`+prunedKey+`":null},"namespaceAnnotations":{"`+prunedKey+`":null}}}`,
		)
		Expect(err).ToNot(HaveOccurred(), out)

		By("pruning the dropped key and leaving every other key in place")
		Eventually(func(g Gomega) {
			g.Expect(nsMetadata(g, "labels")).To(Equal(map[string]string{
				keptKey:    "one",
				foreignKey: foreignValue,
				// Added by Kubernetes on namespace creation.
				"kubernetes.io/metadata.name": targetNamespace,
				// Added by Helm when it creates the release namespace.
				"name": targetNamespace,
			}))

			g.Expect(nsMetadata(g, "annotations")).To(Equal(map[string]string{
				keptKey:    "one",
				foreignKey: foreignValue,
			}))
		}).Should(Succeed())
	})
})

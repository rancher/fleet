package durationvalidation

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const durationMsg = "must be a valid Go duration"

type durationCase struct {
	kind      string
	specField string
	baseSpec  map[string]interface{}
}

var durationCases = []durationCase{
	{"GitRepo", "pollingInterval", map[string]interface{}{"repo": "https://github.com/rancher/fleet-test-data"}},
	{"GitRepo", "imageScanInterval", map[string]interface{}{"repo": "https://github.com/rancher/fleet-test-data"}},
	{"HelmOp", "pollingInterval", map[string]interface{}{}},
	{"ClusterRegistrationToken", "ttl", map[string]interface{}{}},
	{"ImageScan", "interval", map[string]interface{}{"image": "nginx"}},
	{"Schedule", "duration", map[string]interface{}{}},
}

var validDurations = []string{"15s", "5m", "2h", "1h30m", "300ms", "1.5h", "100µs", "0", "0s"}
var invalidDurations = []string{"1d", "1w", "1y", "7d12h", "abc", "5x", "-1h",
	// "5000000h" matches the unit regex but overflows Go's int64-ns duration
	// (~570y > ~292y max); guarded by the duration(self) <= duration('2562047h')
	// bound in the CEL rule, which rejects it with the same friendly message.
	"5000000h"}

func newObj(c durationCase, value string) *unstructured.Unstructured {
	spec := map[string]interface{}{}
	for k, v := range c.baseSpec {
		spec[k] = v
	}
	spec[c.specField] = value

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("fleet.cattle.io/v1alpha1")
	u.SetKind(c.kind)
	u.SetNamespace(namespace)
	u.SetGenerateName("dv-")
	u.Object["spec"] = spec
	return u
}

var _ = Describe("CRD metav1.Duration field validation", func() {
	for _, c := range durationCases {
		Context(fmt.Sprintf("%s spec.%s", c.kind, c.specField), func() {
			for _, v := range invalidDurations {
				It(fmt.Sprintf("rejects %q", v), func() {
					err := k8sClient.Create(ctx, newObj(c, v))
					Expect(err).To(HaveOccurred(),
						"the API server must reject an invalid duration")
					Expect(err.Error()).To(ContainSubstring(durationMsg),
						"rejection must come from the duration CEL rule, got: %s", err.Error())
				})
			}

			for _, v := range validDurations {
				It(fmt.Sprintf("accepts %q", v), func() {
					obj := newObj(c, v)
					Expect(k8sClient.Create(ctx, obj)).To(Succeed(),
						"a valid duration must be accepted")
					DeferCleanup(func() {
						_ = k8sClient.Delete(ctx, obj)
					})
				})
			}
		})
	}
})

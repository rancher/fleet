package summary

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/data"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"
)

var _ = Describe("Summary", func() {
	When("testing kStatusSummarizer", func() {
		It("should deduplicate messages", func() {
			obj := data.Object{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{
							"type":    "Ready",
							"status":  "False",
							"message": "foo; bar; foo; foo; bar",
						},
					},
				},
			}
			summary := kStatusSummarizer(obj, nil, fleetv1.Summary{})
			Expect(summary.Message).To(Equal([]string{"foo; bar"}))
			Expect(summary.Transitioning).To(BeTrue())
			Expect(summary.Error).To(BeFalse())
		})
	})
})

package summary

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/data"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"
)

var _ = Describe("Summary", func() {
	When("testing kStatusSummarizer", func() {
		newObj := func(message string) data.Object {
			return data.Object{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{
							"type":    "Ready",
							"status":  "False",
							"message": message,
						},
					},
				},
			}
		}

		It("should deduplicate messages", func() {
			obj := newObj("message1; message2; message1; message1; message2")
			summary := kStatusSummarizer(obj, nil, fleetv1.Summary{})
			Expect(summary.Message).To(Equal([]string{"message1; message2"}))
			Expect(summary.Transitioning).To(BeTrue())
			Expect(summary.Error).To(BeFalse())
		})

		It("should not alter the message that doesn't contain duplicates", func() {
			obj := newObj("message1; message2; message3")
			summary := kStatusSummarizer(obj, nil, fleetv1.Summary{})
			Expect(summary.Message).To(Equal([]string{"message1; message2; message3"}))
		})

		It("should not deduplicate messages with the wrong separator", func() {
			separators := []string{", ", " ", " , ", "[", "]", "{", "}"}
			for _, sep := range separators {
				obj := newObj("message1" + sep + "message1")
				summary := kStatusSummarizer(obj, nil, fleetv1.Summary{})
				Expect(summary.Message).To(Equal([]string{"message1" + sep + "message1"}))
			}
		})

		It("should not fail on an empty message", func() {
			obj := newObj("")
			summary := kStatusSummarizer(obj, nil, fleetv1.Summary{})
			Expect(summary.Message).To(BeEmpty())
		})
	})
})

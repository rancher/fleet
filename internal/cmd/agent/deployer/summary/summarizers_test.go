package summary

import (
	"os"
	"testing"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/data"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"
	"github.com/stretchr/testify/assert"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCheckErrors(t *testing.T) {
	type input struct {
		data       data.Object
		conditions []Condition
		summary    fleet.Summary
	}

	type output struct {
		summary fleet.Summary
	}

	testCases := []struct {
		name           string
		loadConditions func()
		input          input
		expected       output
	}{
		{
			name: "gvk not detected - summary remains the same",
			input: input{
				data: data.Object{},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
		},
		{
			name: "gvk not found - summary remains the same",
			input: input{
				data: data.Object{
					"APIVersion": "sample.cattle.io/v1",
					"Kind":       "Sample",
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
		},
		{
			name: "gvk found, no conditions provided",
			input: input{
				data: data.Object{
					"APIVersion": "helm.cattle.io/v1",
					"Kind":       "HelmChart",
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
		},
		{
			name: "gvk found, condition not found",
			input: input{
				data: data.Object{
					"APIVersion": "helm.cattle.io/v1",
					"Kind":       "HelmChart",
				},
				conditions: []Condition{
					newCondition("JobFailed", "True", "", ""),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
		},
		{
			name: "gvk found, condition is error",
			input: input{
				data: data.Object{
					"APIVersion": "helm.cattle.io/v1",
					"Kind":       "HelmChart",
				},
				conditions: []Condition{
					newCondition("Failed", "True", "", "Helm Install Error"),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: true,
					Message: []string{
						"Helm Install Error",
					},
				},
			},
		},
		{
			name: "gvk found, condition is not an error",
			input: input{
				data: data.Object{
					"APIVersion": "helm.cattle.io/v1",
					"Kind":       "HelmChart",
				},
				conditions: []Condition{
					newCondition("Failed", "False", "", ""),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
		},
		{
			name: "load conditions - gvk not found",
			input: input{
				data: data.Object{
					"APIVersion": "helm.cattle.io/v1",
					"Kind":       "HelmChart",
				},
				conditions: []Condition{
					newCondition("Failed", "False", "", ""),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			loadConditions: func() {
				os.Setenv(checkGVKErrorMappingEnvVar, `
					[
						{
							"gvk": "sample.cattle.io/v1, Kind=Sample",
							"conditionMappings": [
								{
									"type": "Failed",
									"status": ["True"]
								}
							]
						}
					]
				`)
			},
		},
		{
			name: "load conditions - gvk found - condition is only informational",
			input: input{
				data: data.Object{
					"APIVersion": "sample.cattle.io/v1",
					"Kind":       "Sample",
				},
				conditions: []Condition{
					newCondition("Created", "True", "", ""),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			loadConditions: func() {
				os.Setenv(checkGVKErrorMappingEnvVar, `
					[
						{
							"gvk": "sample.cattle.io/v1, Kind=Sample",
							"conditionMappings": [
								{
									"type": "Created",
									"status": []
								}
							]
						}
					]
				`)
			},
		},
		{
			name: "load conditions - gvk found - is not an error",
			input: input{
				data: data.Object{
					"APIVersion": "sample.cattle.io/v1",
					"Kind":       "Sample",
				},
				conditions: []Condition{
					newCondition("Failed", "False", "", ""),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			loadConditions: func() {
				os.Setenv(checkGVKErrorMappingEnvVar, `
					[
						{
							"gvk": "sample.cattle.io/v1, Kind=Sample",
							"conditionMappings": [
								{
									"type": "Failed",
									"status": ["True"]
								}
							]
						}
					]
				`)
			},
		},
		{
			name: "load conditions - gvk found - is error",
			input: input{
				data: data.Object{
					"APIVersion": "sample.cattle.io/v1",
					"Kind":       "Sample",
				},
				conditions: []Condition{
					newCondition("Failed", "True", "", "Sample Failure"),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: true,
					Message: []string{
						"Sample Failure",
					},
				},
			},
			loadConditions: func() {
				os.Setenv(checkGVKErrorMappingEnvVar, `
					[
						{
							"gvk": "sample.cattle.io/v1, Kind=Sample",
							"conditionMappings": [
								{
									"type": "Failed",
									"status": ["True"]
								}
							]
						}
					]
				`)
			},
		},
		{
			name: "fallback conditions",
			input: input{
				data: data.Object{
					"APIVersion": "fallback.cattle.io/v1",
					"Kind":       "Fallback",
				},
				conditions: []Condition{
					newCondition("Failed", "True", "", "Sample Failure"),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: true,
					Message: []string{
						"Sample Failure",
					},
				},
			},
		},
		{
			name: "condition has error at reason field",
			input: input{
				data: data.Object{
					"APIVersion": "sample.cattle.io/v1",
					"Kind":       "Sample",
				},
				conditions: []Condition{
					newCondition("SampleFailed", "True", "Error", "Error in Reason"),
				},
				summary: fleet.Summary{
					State: "testing",
					Error: false,
				},
			},
			expected: output{
				summary: fleet.Summary{
					State: "testing",
					Error: true,
					Message: []string{
						"Error in Reason",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.loadConditions != nil {
				tc.loadConditions()
			}
			initializeCheckErrors()
			summary := checkErrors(tc.input.data, tc.input.conditions, tc.input.summary)

			assert.Equal(t, tc.expected.summary, summary)
		})
	}

}

func newCondition(conditionType, status, reason, message string) Condition {
	return Condition{
		Object: map[string]interface{}{
			"type":    conditionType,
			"status":  status,
			"reason":  reason,
			"message": message,
		},
	}
}

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
			smr := kStatusSummarizer(obj, nil, fleet.Summary{})
			Expect(smr.Message).To(Equal([]string{"message1; message2"}))
			Expect(smr.Transitioning).To(BeTrue())
			Expect(smr.Error).To(BeFalse())
		})

		It("should not alter the message that doesn't contain duplicates", func() {
			obj := newObj("message1; message2; message3")
			smr := kStatusSummarizer(obj, nil, fleet.Summary{})
			Expect(smr.Message).To(Equal([]string{"message1; message2; message3"}))
		})

		It("should not deduplicate messages with the wrong separator", func() {
			separators := []string{", ", " ", " , ", "[", "]", "{", "}"}
			for _, sep := range separators {
				obj := newObj("message1" + sep + "message1")
				smr := kStatusSummarizer(obj, nil, fleet.Summary{})
				Expect(smr.Message).To(Equal([]string{"message1" + sep + "message1"}))
			}
		})

		It("should not fail on an empty message", func() {
			obj := newObj("")
			smr := kStatusSummarizer(obj, nil, fleet.Summary{})
			Expect(smr.Message).To(BeEmpty())
		})
	})
})

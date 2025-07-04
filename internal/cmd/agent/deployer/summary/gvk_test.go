package summary

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestConditionalTypeStatusErrorMapping_MarshalJSON(t *testing.T) {
	testCases := []struct {
		name        string
		input       ConditionTypeStatusErrorMapping
		expected    []byte
		expectedErr error
	}{
		{
			name: "usual case",
			input: ConditionTypeStatusErrorMapping{
				{Group: "helm.cattle.io", Version: "v1", Kind: "HelmChart"}: {
					"JobCreated": sets.New[metav1.ConditionStatus](),
					"Failed":     sets.New[metav1.ConditionStatus](metav1.ConditionTrue),
				},
			},
			expected: []byte(`
			[
				{
					"gvk": "helm.cattle.io/v1, Kind=HelmChart",
					"conditionMappings": [
						{
							"type": "JobCreated"
						},
						{
							"type": "Failed",
							"status": ["True"]
						}
					]
				}
			]
			`),
			expectedErr: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := json.Marshal(&tc.input)

			if tc.expectedErr == nil {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}

			actual := []conditionTypeStatusJSON{}
			expected := []conditionTypeStatusJSON{}
			assert.NoError(t, json.Unmarshal(output, &actual))
			assert.NoError(t, json.Unmarshal(tc.expected, &expected))

			sort.Slice(actual, func(i, j int) bool { return actual[i].GVK < actual[j].GVK })
			sort.Slice(expected, func(i, j int) bool { return expected[i].GVK < expected[j].GVK })

			for _, act := range actual {
				sort.Slice(act.ConditionMappings, func(i, j int) bool {
					return act.ConditionMappings[i].Type < act.ConditionMappings[j].Type
				})

				for _, mappings := range act.ConditionMappings {
					sort.Slice(mappings.Status, func(i, j int) bool {
						return mappings.Status[i] < mappings.Status[j]
					})
				}
			}

			for _, exp := range expected {
				sort.Slice(exp.ConditionMappings, func(i, j int) bool {
					return exp.ConditionMappings[i].Type < exp.ConditionMappings[j].Type
				})

				for _, mappings := range exp.ConditionMappings {
					sort.Slice(mappings.Status, func(i, j int) bool {
						return mappings.Status[i] < mappings.Status[j]
					})
				}
			}

			assert.Equal(t, expected, actual)
		})
	}
}

func TestConditionalTypeStatusErrorMapping_UnmarshalJSON(t *testing.T) {
	testCases := []struct {
		name            string
		input           []byte
		expected        ConditionTypeStatusErrorMapping
		errorIsExpected bool
	}{
		{
			name: "usual case",
			input: []byte(`
			[
				{
					"gvk": "helm.cattle.io/v1, Kind=HelmChart",
					"conditionMappings": [
						{
							"type": "JobCreated"
						},
						{
							"type": "Failed",
							"status": ["True"]
						}
					]
				}
			]
			`),
			expected: ConditionTypeStatusErrorMapping{
				{Group: "helm.cattle.io", Version: "v1", Kind: "HelmChart"}: {
					"JobCreated": sets.New[metav1.ConditionStatus](),
					"Failed":     sets.New[metav1.ConditionStatus](metav1.ConditionTrue),
				},
			},
			errorIsExpected: false,
		},
		{
			name: "core types (no group)",
			input: []byte(`
			[
				{
					"gvk": "/v1, Kind=Node",
					"conditionMappings": [
						{
							"type": "Ready",
							"status": ["False"]
						}
					]
				}
			]
			`),
			expected: ConditionTypeStatusErrorMapping{
				{Group: "", Version: "v1", Kind: "Node"}: {
					"Ready": sets.New[metav1.ConditionStatus](metav1.ConditionFalse),
				},
			},
			errorIsExpected: false,
		},
		{
			name: "wrong GVK format",
			input: []byte(`
			[
				{
					"gvk": "wrong GVK format",
					"conditionMappings": [
						{
							"type": "Ready",
							"status": ["False"]
						}
					]
				}
			]
			`),
			expected:        ConditionTypeStatusErrorMapping{},
			errorIsExpected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gvkConditionMappings := ConditionTypeStatusErrorMapping{}

			err := json.Unmarshal(tc.input, &gvkConditionMappings)
			if tc.errorIsExpected {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tc.expected, gvkConditionMappings)
		})
	}
}

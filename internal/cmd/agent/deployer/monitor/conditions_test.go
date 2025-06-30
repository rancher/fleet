package monitor

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestExcludeIgnoredConditions(t *testing.T) {
	podInitializedAndNotReady := v1.Pod{Status: v1.PodStatus{
		Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionFalse}, {Type: v1.PodInitialized, Status: v1.ConditionTrue}},
	}}
	uPodInitializedAndNotReady, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&podInitializedAndNotReady)
	if err != nil {
		t.Errorf("can't convert podInitializedAndNotReady to unstructured: %v", err)
	}
	podInitialized := v1.Pod{Status: v1.PodStatus{
		Conditions: []v1.PodCondition{{Type: v1.PodInitialized, Status: v1.ConditionTrue}},
	}}
	uPodInitialized, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&podInitialized)
	if err != nil {
		t.Errorf("can't convert podInitialized to unstructured: %v", err)
	}
	tests := map[string]struct {
		obj           *unstructured.Unstructured
		ignoreOptions *fleet.IgnoreOptions
		expectedObj   *unstructured.Unstructured
		expectedErr   error
	}{
		"nothing is changed with empty IgnoreOptions": {
			obj:           &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			ignoreOptions: &fleet.IgnoreOptions{},
			expectedObj:   &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			expectedErr:   nil,
		},
		"nothing is changed with nil IgnoreOptions": {
			obj:           &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			ignoreOptions: nil,
			expectedObj:   &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			expectedErr:   nil,
		},
		"nothing is changed when IgnoreOptions don't match any condition": {
			obj:           &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			ignoreOptions: &fleet.IgnoreOptions{Conditions: []map[string]string{{"Not": "Found"}}},
			expectedObj:   &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			expectedErr:   nil,
		},
		"'Type: Ready' condition is excluded when IgnoreOptions contains 'Type: Ready' condition": {
			obj:           &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			ignoreOptions: &fleet.IgnoreOptions{Conditions: []map[string]string{{"type": "Ready"}}},
			expectedObj:   &unstructured.Unstructured{Object: uPodInitialized},
			expectedErr:   nil,
		},
		"'Type: Ready' condition is excluded when IgnoreOptions contains 'Type: Ready, status: False' condition": {
			obj:           &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			ignoreOptions: &fleet.IgnoreOptions{Conditions: []map[string]string{{"type": "Ready", "status": "False"}}},
			expectedObj:   &unstructured.Unstructured{Object: uPodInitialized},
			expectedErr:   nil,
		},
		"nothing is changed when IgnoreOptions contains 'type: Ready, status: True' condition": {
			obj:           &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			ignoreOptions: &fleet.IgnoreOptions{Conditions: []map[string]string{{"type": "Ready", "status": "True"}}},
			expectedObj:   &unstructured.Unstructured{Object: uPodInitializedAndNotReady},
			expectedErr:   nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			obj := test.obj
			err := excludeIgnoredConditions(obj, test.ignoreOptions)
			if err != test.expectedErr {
				t.Errorf("expected error doesn't match: expected %v, got %v", test.expectedErr, err)
			}
			if !cmp.Equal(obj, test.expectedObj) {
				t.Errorf("objects don't match: expected %v, got %v", test.expectedObj, obj)
			}
		})
	}
}

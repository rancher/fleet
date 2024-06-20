package normalizers

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestStatusNormalizer_Normalize(t *testing.T) {
	tests := []struct {
		name  string
		obj   runtime.Object
		check func(object runtime.Object) error
	}{
		{
			name: "object with status",
			obj: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "1.2.3.4",
				},
			},
			check: func(obj runtime.Object) error {
				if obj.(*corev1.Pod).Status.PodIP != "" {
					return errors.New("status was not removed")
				}
				return nil
			},
		},
		{
			name:  "object without status",
			obj:   &corev1.ConfigMap{},
			check: func(_ runtime.Object) error { return nil },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			un, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tt.obj)
			if err != nil {
				t.Fatal(err)
			}
			if err := (StatusNormalizer{}).Normalize(&unstructured.Unstructured{Object: un}); err != nil {
				t.Fatal(err)
			}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(un, tt.obj); err != nil {
				t.Fatal(err)
			}
			if err := tt.check(tt.obj); err != nil {
				t.Error(err)
			}
		})
	}
}

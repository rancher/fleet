package normalizers

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// StatusNormalizer removes a top-level "status" fields from the object, if present
type StatusNormalizer struct{}

func (StatusNormalizer) Normalize(un *unstructured.Unstructured) error {
	unstructured.RemoveNestedField(un.Object, "status")
	return nil
}

// Package namespace generates the name of the cluster registration namespace. (fleetcontroller)
package namespace

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func GVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   corev1.SchemeGroupVersion.Group,
		Version: corev1.SchemeGroupVersion.Version,
		Kind:    "Namespace",
	}
}

func RegistrationNamespace(systemNamespace string) string {
	systemRegistrationNamespace := strings.ReplaceAll(systemNamespace, "-system", "-clusters-system")
	if systemRegistrationNamespace == systemNamespace {
		return systemNamespace + "-clusters-system"
	}
	return systemRegistrationNamespace
}

// Package namespace generates the name of the system registration namespace. (fleetcontroller)
//
// Special namespaces in fleet:
// * system namespace: cattle-fleet-system
// * system registration namespace: cattle-fleet-clusters-system
// * cluster registration namespace or "workspace": fleet-local
// * cluster namespace: cluster-${namespace}-${cluster}-${random}

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

// SystemRegistrationNamespace generates the name of the system registration
// namespace from the configured system namespace, e.g.:
// cattle-fleet-system -> cattle-fleet-clusters-system
func SystemRegistrationNamespace(systemNamespace string) string {
	ns := strings.ReplaceAll(systemNamespace, "-system", "-clusters-system")
	if ns == systemNamespace {
		return systemNamespace + "-clusters-system"
	}
	return ns
}

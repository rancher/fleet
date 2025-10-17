package namespaces

import "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

// GetDeploymentNS returns the target namespace for a Fleet deployment.
// A non-empty TargetNamespace in the provided bundle deployment options `o` has precedence over a non-empty
// DefaultNamespace in those same options.
// If no namespace is specified in options, GetDeploymentNS returns the provided defaultNS.
func GetDeploymentNS(defaultNS string, o v1alpha1.BundleDeploymentOptions) string {
	if o.TargetNamespace != "" {
		return o.TargetNamespace
	}

	if o.DefaultNamespace != "" {
		return o.DefaultNamespace
	}

	return defaultNS
}

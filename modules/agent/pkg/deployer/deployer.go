package deployer

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"k8s.io/apimachinery/pkg/runtime"
)

type Resources struct {
	ID               string           `json:"id,omitempty"`
	DefaultNamespace string           `json:"defaultNamespace,omitempty"`
	Objects          []runtime.Object `json:"objects,omitempty"`
}

type DeployedBundle struct {
	// BundleID is the bundle.Name
	BundleID string
	// ReleaseName is actually in the form "namespace/release name"
	ReleaseName string
}

type Deployer interface {
	Deploy(bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions) (*Resources, error)
	ListDeployments() ([]DeployedBundle, error)
	EnsureInstalled(bundleID, resourcesID string) (bool, error)
	Resources(bundleID, resourcesID string) (*Resources, error)
	Delete(bundleID, releaseName string) error
}

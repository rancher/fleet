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

type Deployer interface {
	Deploy(bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions) (*Resources, error)
	ListDeployments() ([]string, error)
	Resources(bundleID, resourcesID string) (*Resources, error)
	Delete(bundleID string) error
}

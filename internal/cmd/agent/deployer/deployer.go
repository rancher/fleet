package deployer

import (
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v2/pkg/kv"
)

type Deployer struct {
	lookup   manifest.Lookup
	deployer *helmdeployer.Helm
}

func New(lookup manifest.Lookup, deployer *helmdeployer.Helm) *Deployer {
	return &Deployer{
		lookup:   lookup,
		deployer: deployer,
	}
}

func (m *Deployer) Delete(bundleDeploymentKey string) error {
	_, name := kv.RSplit(bundleDeploymentKey, "/")
	return m.deployer.Delete(name, "")
}

// Deploy the bundle deployment, i.e. with helmdeployer.
// This loads the manifest and the contents from the upstream cluster.
func (m *Deployer) Deploy(bd *fleet.BundleDeployment) (string, error) {
	if bd.Spec.DeploymentID == bd.Status.AppliedDeploymentID {
		if ok, err := m.deployer.EnsureInstalled(bd.Name, bd.Status.Release); err != nil {
			return "", err
		} else if ok {
			return bd.Status.Release, nil
		}
	}
	manifestID, _ := kv.Split(bd.Spec.DeploymentID, ":")
	manifest, err := m.lookup.Get(manifestID)
	if err != nil {
		return "", err
	}

	manifest.Commit = bd.Labels["fleet.cattle.io/commit"]
	resource, err := m.deployer.Deploy(bd.Name, manifest, bd.Spec.Options)
	if err != nil {
		return "", err
	}

	return resource.ID, nil
}

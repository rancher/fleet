package deployer

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/kv"
	apierror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Manager struct {
	fleetNamespace        string
	bundleDeploymentCache fleetcontrollers.BundleDeploymentCache
	lookup                manifest.Lookup
	deployer              Deployer
	apply                 apply.Apply
}

func NewManager(fleetNamespace string,
	bundleDeploymentCache fleetcontrollers.BundleDeploymentCache,
	lookup manifest.Lookup,
	deployer Deployer,
	apply apply.Apply) *Manager {
	return &Manager{
		fleetNamespace:        fleetNamespace,
		bundleDeploymentCache: bundleDeploymentCache,
		lookup:                lookup,
		deployer:              deployer,
		apply:                 apply.WithDynamicLookup(),
	}
}

func (m *Manager) Cleanup() error {
	ids, err := m.deployer.ListDeployments()
	if err != nil {
		return err
	}

	for _, id := range ids {
		_, err := m.bundleDeploymentCache.Get(m.fleetNamespace, id)
		if apierror.IsNotFound(err) {
			if err := m.deployer.Delete(id); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) Delete(bundleDeploymentKey string) error {
	_, name := kv.RSplit(bundleDeploymentKey, "/")
	return m.deployer.Delete(name)
}

func (m *Manager) Resources(bd *fleet.BundleDeployment) (*Resources, error) {
	resources, err := m.deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		return nil, nil
	}

	plan, err := m.getApply(bd, resources.DefaultNamespace).
		DryRun(resources.Objects...)
	if err != nil {
		return nil, err
	}

	for gvk, keys := range plan.Delete {
		for _, key := range keys {
			u := &unstructured.Unstructured{}
			u.SetGroupVersionKind(gvk)
			u.SetNamespace(key.Namespace)
			u.SetName(key.Name)
			resources.Objects = append(resources.Objects, u)
		}
	}

	return resources, nil
}

func (m *Manager) Deploy(bd *fleet.BundleDeployment) (string, error) {
	if bd.Spec.DeploymentID == bd.Status.AppliedDeploymentID {
		return bd.Status.Release, nil
	}

	manifestID, _ := kv.Split(bd.Spec.DeploymentID, ":")
	manifest, err := m.lookup.Get(manifestID)
	if err != nil {
		return "", err
	}

	resource, err := m.deployer.Deploy(bd.Name, manifest, bd.Spec.Options)
	if err != nil {
		return "", err
	}

	return resource.ID, nil
}

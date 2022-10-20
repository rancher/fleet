package deployer

import (
	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/kv"
	apierror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Manager struct {
	fleetNamespace        string
	defaultNamespace      string
	bundleDeploymentCache fleetcontrollers.BundleDeploymentCache
	lookup                manifest.Lookup
	deployer              *helmdeployer.Helm
	apply                 apply.Apply
	labelPrefix           string
	labelSuffix           string
}

func NewManager(fleetNamespace string,
	defaultNamespace string,
	labelPrefix, labelSuffix string,
	bundleDeploymentCache fleetcontrollers.BundleDeploymentCache,
	lookup manifest.Lookup,
	deployer *helmdeployer.Helm,
	apply apply.Apply) *Manager {
	return &Manager{
		fleetNamespace:        fleetNamespace,
		defaultNamespace:      defaultNamespace,
		labelPrefix:           labelPrefix,
		labelSuffix:           labelSuffix,
		bundleDeploymentCache: bundleDeploymentCache,
		lookup:                lookup,
		deployer:              deployer,
		apply:                 apply.WithDynamicLookup(),
	}
}

// releaseKey returns a deploymentKey from namespace+releaseName
func (m *Manager) releaseKey(bd *fleet.BundleDeployment) string {
	ns := m.defaultNamespace
	if bd.Spec.Options.TargetNamespace != "" {
		ns = bd.Spec.Options.TargetNamespace
	} else if bd.Spec.Options.DefaultNamespace != "" {
		ns = bd.Spec.Options.DefaultNamespace
	}

	if bd.Spec.Options.Helm == nil || bd.Spec.Options.Helm.ReleaseName == "" {
		return ns + "/" + bd.Name
	}
	return ns + "/" + bd.Spec.Options.Helm.ReleaseName
}

func (m *Manager) Cleanup() error {
	deployed, err := m.deployer.ListDeployments()
	if err != nil {
		return err
	}

	for _, deployed := range deployed {
		bundleDeployment, err := m.bundleDeploymentCache.Get(m.fleetNamespace, deployed.BundleID)
		if apierror.IsNotFound(err) {
			// found a helm secret, but no bundle deployment, so uninstall the release
			logrus.Infof("Deleting orphan bundle ID %s, release %s", deployed.BundleID, deployed.ReleaseName)
			if err := m.deployer.Delete(deployed.BundleID, deployed.ReleaseName); err != nil {
				return err
			}

			return nil
		} else if err != nil {
			return err
		}

		key := m.releaseKey(bundleDeployment)
		if key != deployed.ReleaseName {
			// found helm secret and bundle deployment for BundleID, but release name doesn't match, so delete the release
			logrus.Infof("Deleting unknown bundle ID %s, release %s, expecting release %s", deployed.BundleID, deployed.ReleaseName, key)
			if err := m.deployer.Delete(deployed.BundleID, deployed.ReleaseName); err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Manager) Delete(bundleDeploymentKey string) error {
	_, name := kv.RSplit(bundleDeploymentKey, "/")
	return m.deployer.Delete(name, "")
}

// Resources returns the resources that are deployed by the bundle deployment, used by trigger.Watches
func (m *Manager) Resources(bd *fleet.BundleDeployment) (*helmdeployer.Resources, error) {
	resources, err := m.deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		return nil, nil
	}

	plan, err := m.plan(bd, resources.DefaultNamespace, resources.Objects...)
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

// Deploy the bundle deployment, i.e. with helmdeployer
func (m *Manager) Deploy(bd *fleet.BundleDeployment) (string, error) {
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

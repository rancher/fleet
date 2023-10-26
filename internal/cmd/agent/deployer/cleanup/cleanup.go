package cleanup

import (
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/sirupsen/logrus"

	apierror "k8s.io/apimachinery/pkg/api/errors"
)

type Cleanup struct {
	fleetNamespace             string
	defaultNamespace           string
	bundleDeploymentCache      fleetcontrollers.BundleDeploymentCache
	deployer                   *helmdeployer.Helm
	bundleDeploymentController fleetcontrollers.BundleDeploymentController
}

func New(fleetNamespace string,
	defaultNamespace string,
	bundleDeploymentCache fleetcontrollers.BundleDeploymentCache,
	bundleDeploymentController fleetcontrollers.BundleDeploymentController,
	deployer *helmdeployer.Helm) *Cleanup {
	return &Cleanup{
		fleetNamespace:             fleetNamespace,
		defaultNamespace:           defaultNamespace,
		bundleDeploymentCache:      bundleDeploymentCache,
		deployer:                   deployer,
		bundleDeploymentController: bundleDeploymentController,
	}
}

func (m *Cleanup) Cleanup() error {
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

// releaseKey returns a deploymentKey from namespace+releaseName
func (m *Cleanup) releaseKey(bd *fleet.BundleDeployment) string {
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

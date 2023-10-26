package driftdetect

import (
	"github.com/rancher/fleet/internal/cmd/agent/deployer/plan"
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v2/pkg/apply"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type DriftDetect struct {
	defaultNamespace string
	deployer         *helmdeployer.Helm
	apply            apply.Apply
	labelPrefix      string
	labelSuffix      string
}

func New(defaultNamespace string,
	labelPrefix, labelSuffix string,
	deployer *helmdeployer.Helm,
	apply apply.Apply) *DriftDetect {
	return &DriftDetect{
		defaultNamespace: defaultNamespace,
		labelPrefix:      labelPrefix,
		labelSuffix:      labelSuffix,
		deployer:         deployer,
		apply:            apply.WithDynamicLookup(),
	}
}

// AllResources returns the resources that are deployed by the bundle deployment,
// according to the helm release history. It adds to be deleted resources to
// the list, by comparing the desired state to the actual state with apply.
func (m *DriftDetect) AllResources(bd *fleet.BundleDeployment) (*helmdeployer.Resources, error) {
	resources, err := m.deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		return nil, nil
	}

	ns := resources.DefaultNamespace
	if ns == "" {
		ns = m.defaultNamespace
	}
	apply := plan.GetApply(m.apply, plan.Options{
		LabelPrefix:      m.labelPrefix,
		LabelSuffix:      m.labelSuffix,
		DefaultNamespace: ns,
		Name:             bd.Name,
	})

	plan, err := plan.Plan(apply, bd, resources.DefaultNamespace, resources.Objects...)
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

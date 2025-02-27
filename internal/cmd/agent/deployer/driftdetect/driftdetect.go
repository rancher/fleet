package driftdetect

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/cmd/agent/trigger"
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type DriftDetect struct {
	// Trigger watches deployed resources on the local cluster.
	trigger *trigger.Trigger

	desiredset       *desiredset.Client
	defaultNamespace string
	labelPrefix      string
	labelSuffix      string

	driftChan chan event.TypedGenericEvent[*fleet.BundleDeployment]
}

func New(
	trigger *trigger.Trigger,
	desiredset *desiredset.Client,
	defaultNamespace string,
	labelPrefix string,
	labelSuffix string,
	driftChan chan event.TypedGenericEvent[*fleet.BundleDeployment],
) *DriftDetect {
	return &DriftDetect{
		trigger:          trigger,
		desiredset:       desiredset,
		defaultNamespace: defaultNamespace,
		labelPrefix:      labelPrefix,
		labelSuffix:      labelSuffix,
		driftChan:        driftChan,
	}
}

func (d *DriftDetect) Clear(bdKey string) error {
	return d.trigger.Clear(bdKey)
}

// Refresh triggers a sync of all resources of the provided bd which may have drifted from their desired state.
func (d *DriftDetect) Refresh(ctx context.Context, bdKey string, bd *fleet.BundleDeployment, resources *helmdeployer.Resources) error {
	logger := log.FromContext(ctx).WithName("drift-detect").WithValues("initialResourceVersion", bd.ResourceVersion)
	logger.V(1).Info("Refreshing drift detection")

	resources, err := d.allResources(ctx, bd, resources)
	if err != nil {
		return err
	}

	if resources == nil {
		return nil
	}

	handler := func(key string) {
		logger.V(1).Info("Notifying driftdetect reconciler of a resource change", "triggeredBy", key)
		d.driftChan <- event.TypedGenericEvent[*fleet.BundleDeployment]{Object: bd}

	}

	// Adding bundledeployment's resource list to the trigger-controller's watch list
	return d.trigger.OnChange(bdKey, resources.DefaultNamespace, handler, resources.Objects...)
}

// allResources returns the resources that are deployed by the bundle deployment,
// according to the helm release history. It adds to be deleted resources to
// the list, by comparing the desired state to the actual state with apply.
func (d *DriftDetect) allResources(ctx context.Context, bd *fleet.BundleDeployment, resources *helmdeployer.Resources) (*helmdeployer.Resources, error) {
	ns := resources.DefaultNamespace
	if ns == "" {
		ns = d.defaultNamespace
	}

	plan, err := d.desiredset.PlanDelete(ctx, ns, desiredset.GetSetID(bd.Name, d.labelPrefix, d.labelSuffix), resources.Objects...)
	if err != nil {
		return nil, err
	}

	for gvk, keys := range plan {
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

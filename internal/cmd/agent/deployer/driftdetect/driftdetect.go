package driftdetect

import (
	"context"

	"github.com/go-logr/logr"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/cmd/agent/trigger"
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type DriftDetect struct {
	// Trigger watches deployed resources on the local cluster.
	trigger *trigger.Trigger

	upstreamClient client.Client
	upstreamReader client.Reader

	desiredset       *desiredset.Client
	defaultNamespace string
	labelPrefix      string
	labelSuffix      string
}

func New(
	trigger *trigger.Trigger,
	upstreamClient client.Client,
	upstreamReader client.Reader,
	desiredset *desiredset.Client,
	defaultNamespace string,
	labelPrefix string,
	labelSuffix string,
) *DriftDetect {
	return &DriftDetect{
		trigger:          trigger,
		upstreamClient:   upstreamClient,
		upstreamReader:   upstreamReader,
		desiredset:       desiredset,
		defaultNamespace: defaultNamespace,
		labelPrefix:      labelPrefix,
		labelSuffix:      labelSuffix,
	}
}

func (d *DriftDetect) Clear(bdKey string) error {
	return d.trigger.Clear(bdKey)
}

// Refresh triggers a sync of all resources of the provided bd which may have drifted from their desired state.
func (d *DriftDetect) Refresh(ctx context.Context, bdKey string, bd *fleet.BundleDeployment, resources *helmdeployer.Resources) error {
	logger := log.FromContext(ctx).WithName("DriftDetect")
	logger.V(1).Info("Refreshing drift detection")

	resources, err := d.allResources(ctx, bd, resources)
	if err != nil {
		return err
	}

	if resources == nil {
		return nil
	}

	logger.V(1).Info("Adding OnChange for bundledeployment's resource list")
	logger = logger.WithValues("key", bdKey, "initialResourceVersion", bd.ResourceVersion)

	handleID := int(bd.Generation)
	handler := func(key string) {
		logger := logger.WithValues("handleID", handleID, "triggered by", key)
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Can't enqueue directly, update bundledeployment instead
			return d.requeueBD(logger, handleID, bd.Namespace, bd.Name)
		})
		if err != nil {
			logger.Error(err, "Failed to trigger bundledeployment", "error", err)
			return
		}

	}
	return d.trigger.OnChange(bdKey, resources.DefaultNamespace, handler, resources.Objects...)
}

func (d *DriftDetect) requeueBD(logger logr.Logger, handleID int, namespace string, name string) error {
	bd := &fleet.BundleDeployment{}

	err := d.upstreamReader.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, bd)
	if apierrors.IsNotFound(err) {
		logger.Info("Bundledeployment is not found, can't trigger refresh")
		return nil
	}
	if err != nil {
		logger.Error(err, "Failed to get bundledeployment, can't trigger refresh")
		return nil
	}

	logger = logger.WithValues("resourceVersion", bd.ResourceVersion)
	logger.V(1).Info("Going to update bundledeployment to trigger re-sync")

	// This mechanism of triggering requeues for changes is not ideal.
	// It's a workaround since we can't enqueue directly from the trigger
	// mini controller. Triggering via a status update is expensive.
	// It's hard to compute a stable hash to make this idempotent, because
	// the hash would need to be computed over the whole change. We can't
	// just use the resource version of the bundle deployment. We would
	// need to look at the deployed resources and compute a hash over them.
	// However this status update happens for every changed resource, maybe
	// multiple times per resource. It will also trigger on a resync.
	bd.Status.SyncGeneration = &[]int64{int64(handleID)}[0]

	err = d.upstreamClient.Status().Update(context.Background(), bd)
	if err != nil {
		logger.V(1).Info("Retry to update bundledeployment, couldn't update status to trigger re-sync", "conflict", apierrors.IsConflict(err), "error", err)
	}
	return err
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

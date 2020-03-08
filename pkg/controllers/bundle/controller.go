package bundle

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	"github.com/rancher/fleet/pkg/target"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/relatedresource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	maxNew = 50
)

type handler struct {
	targets *target.Manager
	bundles fleetcontrollers.BundleController
}

func Register(ctx context.Context,
	apply apply.Apply,
	targets *target.Manager,
	bundles fleetcontrollers.BundleController,
	clusters fleetcontrollers.ClusterController,
	bundleDeployments fleetcontrollers.BundleDeploymentController,
) {
	h := &handler{
		targets: targets,
		bundles: bundles,
	}

	fleetcontrollers.RegisterBundleGeneratingHandler(ctx,
		bundles,
		apply.WithCacheTypes(bundleDeployments),
		"Processed",
		"bundle",
		h.OnBundleChange,
		&generic.GeneratingHandlerOptions{
			AllowClusterScoped: true,
		})

	relatedresource.Watch(ctx, "app", h.resolveApp, bundles, bundleDeployments)
	clusters.OnChange(ctx, "app", h.OnClusterChange)
}

func (h *handler) resolveApp(_ string, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if ad, ok := obj.(*fleet.BundleDeployment); ok {
		ns, name := h.targets.BundleForDeployment(ad)
		if ns != "" && name != "" {
			return []relatedresource.Key{
				{
					Namespace: ns,
					Name:      name,
				},
			}, nil
		}
	}
	return nil, nil
}

func (h *handler) OnClusterChange(_ string, cluster *fleet.Cluster) (*fleet.Cluster, error) {
	if cluster == nil {
		return nil, nil
	}

	bundles, err := h.targets.BundlesForCluster(cluster)
	if err != nil {
		return nil, err
	}

	for _, bundle := range bundles {
		h.bundles.Enqueue(bundle.Namespace, bundle.Name)
	}

	return cluster, nil
}

func (h *handler) OnBundleChange(bundle *fleet.Bundle, status fleet.BundleStatus) ([]runtime.Object, fleet.BundleStatus, error) {
	targets, err := h.targets.Targets(bundle)
	if err != nil {
		return nil, status, err
	}

	if err := h.calculateChanges(&status, targets); err != nil {
		return nil, status, err
	}

	summary.SetReadyConditions(&status, status.Summary)
	return toRuntimeObjects(targets), status, nil
}

func toRuntimeObjects(targets []*target.Target) (result []runtime.Object) {
	for _, target := range targets {
		if target.Deployment == nil {
			continue
		}

		result = append(result, &fleet.BundleDeployment{
			ObjectMeta: v1.ObjectMeta{
				Name:      target.Deployment.Name,
				Namespace: target.Deployment.Namespace,
				Labels:    target.Deployment.Labels,
			},
			Spec: target.Deployment.Spec,
		})
	}

	return
}

func (h *handler) calculateChanges(status *fleet.BundleStatus, targets []*target.Target) (err error) {
	// reset
	status.MaxNew = maxNew
	status.Summary = fleet.BundleSummary{}
	status.Unavailable = 0
	status.NewlyCreated = 0
	status.MaxUnavailable, err = target.MaxUnavailable(targets)
	if err != nil {
		return err
	}

	for _, target := range targets {
		if target.Deployment == nil {
			newTarget(target, status)
		}
		if target.Deployment != nil {
			target.Deployment.Spec.StagedOptions = target.Options
			target.Deployment.Spec.StagedDeploymentID = target.DeploymentID
		}
	}

	status.Unavailable = target.Unavailable(targets)

	for _, currentTarget := range targets {
		updateManifest(currentTarget, status)
		cluster := currentTarget.Cluster.Namespace + "/" + currentTarget.Cluster.Name
		summary.IncrementState(&status.Summary, cluster, currentTarget.State(), currentTarget.Message())
		status.Summary.DesiredReady++
	}

	return nil
}

func updateManifest(t *target.Target, status *fleet.BundleStatus) {
	if t.Deployment != nil &&
		!t.IsPaused() &&
		t.Deployment.Spec.StagedDeploymentID != "" &&
		t.Deployment.Spec.DeploymentID != t.Deployment.Spec.StagedDeploymentID &&
		(status.Unavailable < status.MaxUnavailable || target.IsUnavailable(t.Deployment)) {
		if !target.IsUnavailable(t.Deployment) {
			status.Unavailable++
		}
		t.Deployment.Spec.DeploymentID = t.Deployment.Spec.StagedDeploymentID
		t.Deployment.Spec.Options = t.Deployment.Spec.StagedOptions
	}
}

func newTarget(target *target.Target, status *fleet.BundleStatus) {
	if status.NewlyCreated >= status.MaxNew {
		return
	}

	status.NewlyCreated++
	target.AssignNewDeployment()
}

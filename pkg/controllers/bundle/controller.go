package bundle

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/controllers/git"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	"github.com/rancher/fleet/pkg/target"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/relatedresource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	maxNew = 50
)

type handler struct {
	targets *target.Manager
	gitRepo fleetcontrollers.GitRepoCache
	bundles fleetcontrollers.BundleController
}

func Register(ctx context.Context,
	apply apply.Apply,
	targets *target.Manager,
	bundles fleetcontrollers.BundleController,
	clusters fleetcontrollers.ClusterController,
	gitRepo fleetcontrollers.GitRepoCache,
	bundleDeployments fleetcontrollers.BundleDeploymentController,
) {
	h := &handler{
		targets: targets,
		bundles: bundles,
		gitRepo: gitRepo,
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
	bundles.OnChange(ctx, "bundle-orphan", h.OnPurgeOrphaned)
}

func (h *handler) resolveApp(_ string, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if ad, ok := obj.(*fleet.BundleDeployment); ok {
		ns, name := h.targets.BundleFromDeployment(ad)
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

func (h *handler) OnPurgeOrphaned(key string, bundle *fleet.Bundle) (*fleet.Bundle, error) {
	if bundle == nil {
		return bundle, nil
	}

	repo := bundle.Labels[git.RepoLabel]
	if repo == "" {
		return nil, nil
	}

	_, err := h.gitRepo.Get(bundle.Namespace, repo)
	if apierrors.IsNotFound(err) {
		return nil, h.bundles.Delete(bundle.Namespace, bundle.Name, nil)
	} else if err != nil {
		return nil, err
	}

	return bundle, nil
}

func (h *handler) OnBundleChange(bundle *fleet.Bundle, status fleet.BundleStatus) ([]runtime.Object, fleet.BundleStatus, error) {
	targets, err := h.targets.Targets(bundle)
	if err != nil {
		return nil, status, err
	}

	if err := h.calculateChanges(&status, targets); err != nil {
		return nil, status, err
	}

	summary.SetReadyConditions(&status, "Cluster", status.Summary)
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

func (h *handler) calculateChanges(status *fleet.BundleStatus, allTargets []*target.Target) (err error) {
	// reset
	status.MaxNew = maxNew
	status.Summary = fleet.BundleSummary{}
	status.PartitionStatus = nil
	status.Unavailable = 0
	status.NewlyCreated = 0
	status.Summary = target.Summary(allTargets)
	status.Unavailable = target.Unavailable(allTargets)
	status.MaxUnavailable, err = target.MaxUnavailable(allTargets)
	if err != nil {
		return err
	}

	partitions, err := target.Partitions(allTargets)
	if err != nil {
		return err
	}

	status.UnavailablePartitions = 0
	status.MaxUnavailablePartitions, err = target.MaxUnavailablePartitions(partitions, allTargets)
	if err != nil {
		return err
	}

	for _, partition := range partitions {
		for _, target := range partition.Targets {
			if target.Deployment == nil {
				newTarget(target, status)
			}
			if target.Deployment != nil {
				target.Deployment.Spec.StagedOptions = target.Options
				target.Deployment.Spec.StagedDeploymentID = target.DeploymentID
			}
		}

		for _, currentTarget := range partition.Targets {
			updateManifest(currentTarget, status, &partition.Status)
		}

		if target.IsPartitionUnavailable(&partition.Status, partition.Targets) {
			status.UnavailablePartitions++
		}

		if status.UnavailablePartitions > status.MaxUnavailablePartitions {
			break
		}
	}

	for _, partition := range partitions {
		status.PartitionStatus = append(status.PartitionStatus, partition.Status)
	}

	return nil
}

func updateManifest(t *target.Target, status *fleet.BundleStatus, partitionStatus *fleet.PartitionStatus) {
	if t.Deployment != nil &&
		// Not Paused
		!t.IsPaused() &&
		// Has been staged
		t.Deployment.Spec.StagedDeploymentID != "" &&
		// Is out of sync
		t.Deployment.Spec.DeploymentID != t.Deployment.Spec.StagedDeploymentID &&
		// Global max unavailable not reached
		(status.Unavailable < status.MaxUnavailable || target.IsUnavailable(t.Deployment)) &&
		// Partition max unavailable not reached
		(partitionStatus.Unavailable < partitionStatus.MaxUnavailable || target.IsUnavailable(t.Deployment)) {
		if !target.IsUnavailable(t.Deployment) {
			// If this was previously available, now increment unavailable count. "Upgrading" is treated as unavailable.
			status.Unavailable++
			partitionStatus.Unavailable++
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

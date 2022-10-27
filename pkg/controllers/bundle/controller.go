// Package bundle registers a controller for Bundle objects. (fleetcontroller)
package bundle

import (
	"context"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/options"
	"github.com/rancher/fleet/pkg/summary"
	"github.com/rancher/fleet/pkg/target"

	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/relatedresource"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	maxNew = 50
)

type handler struct {
	targets           *target.Manager
	gitRepo           fleetcontrollers.GitRepoCache
	images            fleetcontrollers.ImageScanController
	bundles           fleetcontrollers.BundleController
	bundleDeployments fleetcontrollers.BundleDeploymentController
	mapper            meta.RESTMapper
}

func Register(ctx context.Context,
	apply apply.Apply,
	mapper meta.RESTMapper,
	targets *target.Manager,
	bundles fleetcontrollers.BundleController,
	clusters fleetcontrollers.ClusterController,
	images fleetcontrollers.ImageScanController,
	gitRepo fleetcontrollers.GitRepoCache,
	bundleDeployments fleetcontrollers.BundleDeploymentController,
) {
	h := &handler{
		mapper:            mapper,
		targets:           targets,
		bundles:           bundles,
		bundleDeployments: bundleDeployments,
		images:            images,
		gitRepo:           gitRepo,
	}

	// A generating handler returns a list of objects to be created and
	// updates the given condition in the status of the object.
	// This handler is triggered for bundles.OnChange
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
	images.OnChange(ctx, "imagescan-orphan", h.OnPurgeOrphanedImageScan)
}

func (h *handler) resolveApp(_ string, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if ad, ok := obj.(*fleet.BundleDeployment); ok {
		ns, name := h.targets.BundleFromDeployment(ad)
		if ns != "" && name != "" {
			logrus.Debugf("enqueue bundle %s/%s for bundledeployment %s change", ns, name, ad.Name)
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
	logrus.Debugf("OnClusterChange for cluster '%s', checking which bundles to enqueue or cleanup", cluster.Name)
	start := time.Now()

	bundlesToRefresh, bundlesToCleanup, err := h.targets.BundlesForCluster(cluster)
	if err != nil {
		return nil, err
	}
	for _, bundle := range bundlesToCleanup {
		bundleDeployments, err := h.targets.GetBundleDeploymentsForBundleInCluster(bundle, cluster)
		if err != nil {
			return nil, err
		}
		for _, bundleDeployment := range bundleDeployments {
			logrus.Debugf("cleaning up bundleDeployment %v in namespace %v not matching the cluster: %v", bundleDeployment.Name, bundleDeployment.Namespace, cluster.Name)
			err := h.bundleDeployments.Delete(bundleDeployment.Namespace, bundleDeployment.Name, nil)
			if err != nil {
				logrus.Debugf("deleting bundleDeployment returned an error: %v", err)
			}
		}
	}

	for _, bundle := range bundlesToRefresh {
		h.bundles.Enqueue(bundle.Namespace, bundle.Name)
	}

	elapsed := time.Since(start)
	logrus.Debugf("OnClusterChange for cluster '%s' took %s", cluster.Name, elapsed)

	return cluster, nil
}

func (h *handler) OnPurgeOrphaned(key string, bundle *fleet.Bundle) (*fleet.Bundle, error) {
	if bundle == nil {
		return bundle, nil
	}
	logrus.Debugf("OnPurgeOrphaned for bundle '%s' change, checking if gitrepo still exists", bundle.Name)

	repo := bundle.Labels[fleet.RepoLabel]
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

func (h *handler) OnPurgeOrphanedImageScan(key string, image *fleet.ImageScan) (*fleet.ImageScan, error) {
	if image == nil || image.DeletionTimestamp != nil {
		return image, nil
	}
	logrus.Debugf("OnPurgeOrphanedImageScan for image '%s' change, checking if gitrepo still exists", image.Name)

	repo := image.Spec.GitRepoName

	_, err := h.gitRepo.Get(image.Namespace, repo)
	if apierrors.IsNotFound(err) {
		return nil, h.images.Delete(image.Namespace, image.Name, nil)
	} else if err != nil {
		return nil, err
	}

	return image, nil
}

func (h *handler) OnBundleChange(bundle *fleet.Bundle, status fleet.BundleStatus) ([]runtime.Object, fleet.BundleStatus, error) {
	logrus.Debugf("OnBundleChange for bundle '%s', checking targets, calculating changes, building objects", bundle.Name)
	start := time.Now()

	targets, err := h.targets.Targets(bundle)
	if err != nil {
		return nil, status, err
	}

	if err := h.calculateChanges(&status, targets); err != nil {
		return nil, status, err
	}

	if err := setResourceKey(&status, bundle, h.isNamespaced, status.ObservedGeneration != bundle.Generation); err != nil {
		return nil, status, err
	}

	summary.SetReadyConditions(&status, "Cluster", status.Summary)
	status.ObservedGeneration = bundle.Generation

	objs := toRuntimeObjects(targets, bundle)

	elapsed := time.Since(start)
	logrus.Debugf("OnBundleChange for bundle '%s' took %s", bundle.Name, elapsed)

	return objs, status, nil
}

func (h *handler) isNamespaced(gvk schema.GroupVersionKind) bool {
	mapping, err := h.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return true
	}
	return mapping.Scope.Name() == meta.RESTScopeNameNamespace
}

func setResourceKey(status *fleet.BundleStatus, bundle *fleet.Bundle, isNSed func(schema.GroupVersionKind) bool, set bool) error {
	if !set {
		return nil
	}
	bundleMap := map[fleet.ResourceKey]struct{}{}
	m, err := manifest.New(&bundle.Spec)
	if err != nil {
		return err
	}

	for i := range bundle.Spec.Targets {
		opts := options.Calculate(&bundle.Spec, &bundle.Spec.Targets[i])
		objs, err := helmdeployer.Template(bundle.Name, m, opts)
		if err != nil {
			return err
		}

		for _, obj := range objs {
			m, err := meta.Accessor(obj)
			if err != nil {
				return err
			}
			key := fleet.ResourceKey{
				Namespace: m.GetNamespace(),
				Name:      m.GetName(),
			}
			gvk := obj.GetObjectKind().GroupVersionKind()
			if key.Namespace == "" && isNSed(gvk) {
				if opts.DefaultNamespace == "" {
					key.Namespace = "default"
				} else {
					key.Namespace = opts.DefaultNamespace
				}
			}
			key.APIVersion, key.Kind = gvk.ToAPIVersionAndKind()
			bundleMap[key] = struct{}{}
		}
	}
	keys := []fleet.ResourceKey{}
	for k := range bundleMap {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		keyi := keys[i]
		keyj := keys[j]
		if keyi.APIVersion != keyj.APIVersion {
			return keyi.APIVersion < keyj.APIVersion
		}
		if keyi.Kind != keyj.Kind {
			return keyi.Kind < keyj.Kind
		}
		if keyi.Namespace != keyj.Namespace {
			return keyi.Namespace < keyj.Namespace
		}
		if keyi.Name != keyj.Name {
			return keyi.Name < keyj.Name
		}
		return false
	})
	status.ResourceKey = keys

	return nil
}

func toRuntimeObjects(targets []*target.Target, bundle *fleet.Bundle) (result []runtime.Object) {
	for _, target := range targets {
		if target.Deployment == nil {
			continue
		}
		dp := &fleet.BundleDeployment{
			ObjectMeta: v1.ObjectMeta{
				Name:      target.Deployment.Name,
				Namespace: target.Deployment.Namespace,
				Labels:    target.Deployment.Labels,
			},
			Spec: target.Deployment.Spec,
		}
		dp.Spec.DependsOn = bundle.Spec.DependsOn
		result = append(result, dp)
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

// Package bundle registers a controller for Bundle objects.
package bundle

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/controller/options"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v2/pkg/apply"
	"github.com/rancher/wrangler/v2/pkg/generic"
	"github.com/rancher/wrangler/v2/pkg/relatedresource"

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
			AllowClusterScoped:            true,
			UniqueApplyForResourceVersion: true,
		})

	relatedresource.Watch(ctx, "app", h.resolveBundle, bundles, bundleDeployments)
	clusters.OnChange(ctx, "app", h.OnClusterChange)
}

func (h *handler) resolveBundle(_ string, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
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

func (h *handler) OnBundleChange(bundle *fleet.Bundle, status fleet.BundleStatus) ([]runtime.Object, fleet.BundleStatus, error) {
	logrus.Debugf("OnBundleChange for bundle '%s', checking targets, calculating changes, building objects", bundle.Name)
	start := time.Now()

	manifest, err := manifest.New(bundle.Spec.Resources)
	if err != nil {
		return nil, status, err
	}

	// this does not need to happen after merging the
	// BundleDeploymentOptions, since 'fleet apply' already put the right
	// resources into bundle.Spec.Resources
	if _, err := h.targets.StoreManifest(manifest); err != nil {
		return nil, status, err
	}

	matchedTargets, err := h.targets.Targets(bundle, manifest)
	if err != nil {
		return nil, status, err
	}

	if err := h.updateStatusAndTargets(&status, matchedTargets); err != nil {
		updateDisplay(&status)
		return nil, status, err
	}

	if status.ObservedGeneration != bundle.Generation {
		if err := setResourceKey(&status, bundle, manifest, h.isNamespaced); err != nil {
			updateDisplay(&status)
			return nil, status, err
		}
	}

	summary.SetReadyConditions(&status, "Cluster", status.Summary)
	status.ObservedGeneration = bundle.Generation

	objs := bundleDeployments(matchedTargets, bundle)

	elapsed := time.Since(start)

	updateDisplay(&status)

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

// setResourceKey updates status.ResourceKey from the bundle, by running helm template (does not mutate bundle)
func setResourceKey(status *fleet.BundleStatus, bundle *fleet.Bundle, manifest *manifest.Manifest, isNSed func(schema.GroupVersionKind) bool) error {
	seen := map[fleet.ResourceKey]struct{}{}

	// iterate over the defined targets, from "targets.yaml", not the
	// actually matched targets to avoid duplicates
	for i := range bundle.Spec.Targets {
		opts := options.Merge(bundle.Spec.BundleDeploymentOptions, bundle.Spec.Targets[i].BundleDeploymentOptions)
		objs, err := helmdeployer.Template(bundle.Name, manifest, opts)
		if err != nil {
			logrus.Infof("While calculating status.ResourceKey, error running helm template for bundle %s with target options from %s: %v", bundle.Name, bundle.Spec.Targets[i].Name, err)
			continue
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
			seen[key] = struct{}{}
		}
	}

	keys := []fleet.ResourceKey{}
	for k := range seen {
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

// bundleDeployments copies BundleDeployments out of targets and into a new slice of runtime.Object
// discarding Status, replacing DependsOn with the bundle's DependsOn (pure function) and replacing the labels with the
// bundle's labels
func bundleDeployments(targets []*target.Target, bundle *fleet.Bundle) (result []runtime.Object) {
	for _, target := range targets {
		if target.Deployment == nil {
			continue
		}
		// NOTE we don't use the existing BundleDeployment, we discard annotations, status, etc
		// copy labels from Bundle as they might have changed
		dp := &fleet.BundleDeployment{
			ObjectMeta: v1.ObjectMeta{
				Name:      target.Deployment.Name,
				Namespace: target.Deployment.Namespace,
				Labels:    target.BundleDeploymentLabels(target.Cluster.Namespace, target.Cluster.Name),
			},
			Spec: target.Deployment.Spec,
		}
		dp.Spec.Paused = target.IsPaused()
		dp.Spec.DependsOn = bundle.Spec.DependsOn
		dp.Spec.CorrectDrift = target.Options.CorrectDrift
		result = append(result, dp)
	}

	return
}

// updateStatusAndTargets recomputes status, including partitions, from data in allTargets
// it creates Deployments in allTargets if they are missing
// it updates Deployments in allTargets if they are out of sync (DeploymentID != StagedDeploymentID)
func (h *handler) updateStatusAndTargets(status *fleet.BundleStatus, allTargets []*target.Target) (err error) {
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
			// for a new bundledeployment, only stage the first maxNew (50) targets
			if target.Deployment == nil && status.NewlyCreated < status.MaxNew {
				status.NewlyCreated++
				target.Deployment = &fleet.BundleDeployment{
					ObjectMeta: v1.ObjectMeta{
						Name:      target.Bundle.Name,
						Namespace: target.Cluster.Status.Namespace,
						Labels:    target.BundleDeploymentLabels(target.Cluster.Namespace, target.Cluster.Name),
					},
				}
			}
			// stage targets that have a Deployment struct
			if target.Deployment != nil {
				// NOTE merged options from targets.Targets() are set to be staged
				target.Deployment.Spec.StagedOptions = target.Options
				target.Deployment.Spec.StagedDeploymentID = target.DeploymentID
			}
		}

		for _, currentTarget := range partition.Targets {
			// NOTE this will propagate the staged, merged options to the current deployment
			updateTarget(currentTarget, status, &partition.Status)
		}

		if target.UpdateStatusUnavailable(&partition.Status, partition.Targets) {
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

// updateTarget will update DeploymentID and Options for the target to the
// staging values, if it's in a deployable state
func updateTarget(t *target.Target, status *fleet.BundleStatus, partitionStatus *fleet.PartitionStatus) {
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

func updateDisplay(status *fleet.BundleStatus) {
	status.Display.ReadyClusters = fmt.Sprintf("%d/%d",
		status.Summary.Ready,
		status.Summary.DesiredReady)
	status.Display.State = string(summary.GetSummaryState(status.Summary))
}

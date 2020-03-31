package cluster

import (
	"context"
	"fmt"
	"sort"

	"github.com/rancher/fleet/pkg/controllers/sharedindex"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	clusterByNamespace = "clusterByNamespace"
)

type handler struct {
	apply            apply.Apply
	managedClusters  fleetcontrollers.ClusterCache
	clusterGroups    fleetcontrollers.ClusterGroupCache
	bundleDeployment fleetcontrollers.BundleDeploymentCache
}

func Register(ctx context.Context,
	bundleDeployment fleetcontrollers.BundleDeploymentController,
	clusterGroups fleetcontrollers.ClusterGroupCache,
	managedClusters fleetcontrollers.ClusterController,
	namespaces corecontrollers.NamespaceController, apply apply.Apply) {

	h := &handler{
		apply:            apply.WithCacheTypes(managedClusters),
		clusterGroups:    clusterGroups,
		managedClusters:  managedClusters.Cache(),
		bundleDeployment: bundleDeployment.Cache(),
	}

	fleetcontrollers.RegisterClusterGeneratingHandler(ctx,
		managedClusters,
		apply.WithCacheTypes(namespaces),
		"Processed",
		"managed-cluster",
		h.OnClusterChanged,
		&generic.GeneratingHandlerOptions{
			AllowClusterScoped: true,
		})

	managedClusters.Cache().AddIndexer(clusterByNamespace, func(obj *fleet.Cluster) (strings []string, err error) {
		if obj.Status.Namespace == "" {
			return nil, nil
		}
		return []string{obj.Status.Namespace}, nil
	})

	relatedresource.Watch(ctx, "managed-cluster", h.findClusters, managedClusters, bundleDeployment)
}

func (h *handler) findClusters(_, _ string, obj runtime.Object) (result []relatedresource.Key, _ error) {
	if ad, ok := obj.(*fleet.BundleDeployment); ok {
		clusters, err := h.managedClusters.GetByIndex(clusterByNamespace, ad.Namespace)
		if err != nil {
			return nil, err
		}
		for _, cluster := range clusters {
			result = append(result, relatedresource.Key{
				Namespace: cluster.Namespace,
				Name:      cluster.Name,
			})
		}
	}

	return result, nil
}

func (h *handler) OnClusterChanged(cluster *fleet.Cluster, status fleet.ClusterStatus) ([]runtime.Object, fleet.ClusterStatus, error) {
	if cluster.DeletionTimestamp != nil {
		return nil, status, nil
	}

	cgs, err := h.clusterGroups.GetByIndex(sharedindex.ClusterGroupByNamespace, cluster.Namespace)
	if err != nil {
		return nil, status, err
	}
	if len(cgs) == 0 {
		return nil, status, fmt.Errorf("failed to find cluster group for namespace %s", cluster.Namespace)
	}

	bundleDeployments, err := h.bundleDeployment.List(status.Namespace, labels.Everything())
	if err != nil {
		return nil, status, err
	}

	status.Namespace = name.SafeConcatName(cluster.Namespace, cluster.Name)
	status.ClusterGroupName = cgs[0].Name
	status.ClusterGroupNamespace = cgs[0].Namespace
	status.Summary = fleet.BundleSummary{}

	sort.Slice(bundleDeployments, func(i, j int) bool {
		return bundleDeployments[i].Name < bundleDeployments[j].Name
	})

	for _, app := range bundleDeployments {
		state := summary.GetDeploymentState(app)
		summary.IncrementState(&status.Summary, app.Name, state, summary.MessageFromDeployment(app))
		status.Summary.DesiredReady++
	}

	summary.SetReadyConditions(&status, status.Summary)
	return []runtime.Object{
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: status.Namespace,
				Annotations: map[string]string{
					fleet.ClusterNamespaceAnnotation: cluster.Namespace,
					fleet.ClusterAnnotation:          cluster.Name,
					fleet.ManagedAnnotation:          "true",
				},
			},
		},
	}, status, nil
}

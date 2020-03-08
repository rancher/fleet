package clustergroup

import (
	"context"
	"sort"

	"github.com/rancher/fleet/pkg/controllers/sharedindex"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/controllers/role"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	rbaccontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	clusterGroups fleetcontrollers.ClusterGroupCache
	clusters      fleetcontrollers.ClusterCache
}

func Register(ctx context.Context,
	apply apply.Apply,
	namespaces corecontrollers.NamespaceController,
	roles rbaccontrollers.RoleController,
	clusters fleetcontrollers.ClusterCache,
	clusterGroups fleetcontrollers.ClusterGroupController,
	managedClusters fleetcontrollers.ClusterController) {

	h := &handler{
		clusterGroups: clusterGroups.Cache(),
		clusters:      clusters,
	}

	relatedresource.Watch(ctx, "cluster-group", h.findClusterGroup, clusterGroups, managedClusters)

	fleetcontrollers.RegisterClusterGroupGeneratingHandler(ctx,
		clusterGroups,
		apply.
			WithCacheTypes(namespaces, roles),
		"Processed",
		"cluster-group",
		h.OnClusterGroup,
		&generic.GeneratingHandlerOptions{
			AllowClusterScoped: true,
		})
}

func (h *handler) findClusterGroup(_, _ string, obj runtime.Object) (result []relatedresource.Key, err error) {
	if c, ok := obj.(*fleet.Cluster); ok {
		cgs, err := h.clusterGroups.GetByIndex(sharedindex.ClusterGroupByNamespace, c.Namespace)
		if err != nil {
			return nil, err
		}
		result = make([]relatedresource.Key, 0, len(cgs))
		for _, cg := range cgs {
			result = append(result, relatedresource.Key{
				Namespace: cg.Namespace,
				Name:      cg.Name,
			})
		}
	}
	return
}

func (h *handler) OnClusterGroup(clusterGroup *fleet.ClusterGroup, status fleet.ClusterGroupStatus) ([]runtime.Object, fleet.ClusterGroupStatus, error) {
	status.Namespace = name.SafeConcatName(clusterGroup.Namespace, clusterGroup.Name, "group")

	clusters, err := h.clusters.List(clusterGroup.Status.Namespace, labels.Everything())
	if err != nil {
		return nil, status, err
	}

	status.Summary = fleet.BundleSummary{}
	status.ClusterCount = 0
	status.NonReadyClusterCount = 0
	status.NonReadyClusters = nil

	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Name < clusters[j].Name
	})

	for _, cluster := range clusters {
		summary.Increment(&status.Summary, cluster.Status.Summary)
		status.ClusterCount++
		if !summary.IsReady(cluster.Status.Summary) {
			status.NonReadyClusterCount++
			if len(status.NonReadyClusters) < 10 {
				status.NonReadyClusters = append(status.NonReadyClusters, cluster.Name)
			}
		}
	}

	summary.SetReadyConditions(&status, status.Summary)
	return []runtime.Object{
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation:               "true",
					fleet.ClusterGroupAnnotation:          clusterGroup.Name,
					fleet.ClusterGroupNamespaceAnnotation: clusterGroup.Namespace,
				},
			},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      role.AgentCredentialRoleName,
				Namespace: status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
		},
	}, status, nil
}

package cluster

import (
	"context"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	apply            apply.Apply
	clusters         fleetcontrollers.ClusterCache
	clusterGroups    fleetcontrollers.ClusterGroupCache
	bundleDeployment fleetcontrollers.BundleDeploymentCache
}

func Register(ctx context.Context,
	bundleDeployment fleetcontrollers.BundleDeploymentController,
	clusterGroups fleetcontrollers.ClusterGroupCache,
	clusters fleetcontrollers.ClusterController,
	namespaces corecontrollers.NamespaceController, apply apply.Apply) {

	h := &handler{
		apply:            apply,
		clusterGroups:    clusterGroups,
		clusters:         clusters.Cache(),
		bundleDeployment: bundleDeployment.Cache(),
	}

	fleetcontrollers.RegisterClusterGeneratingHandler(ctx,
		clusters,
		apply,
		"Processed",
		"managed-cluster",
		h.OnClusterChanged,
		&generic.GeneratingHandlerOptions{
			AllowClusterScoped: true,
		})

	relatedresource.Watch(ctx, "managed-cluster", h.findClusters(namespaces.Cache()), clusters, bundleDeployment)
}

func (h *handler) findClusters(namespaces corecontrollers.NamespaceCache) relatedresource.Resolver {
	return func(namespace, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
		if _, ok := obj.(*fleet.BundleDeployment); !ok {
			return nil, nil
		}

		ns, err := namespaces.Get(namespace)
		if err != nil {
			return nil, nil
		}

		clusterNS := ns.Annotations[fleet.ClusterNamespaceAnnotation]
		clusterName := ns.Annotations[fleet.ClusterAnnotation]
		if clusterNS == "" || clusterName == "" {
			return nil, nil
		}
		return []relatedresource.Key{
			{
				Namespace: clusterNS,
				Name:      clusterName,
			},
		}, nil
	}
}

func (h *handler) OnClusterChanged(cluster *fleet.Cluster, status fleet.ClusterStatus) ([]runtime.Object, fleet.ClusterStatus, error) {
	if cluster.DeletionTimestamp != nil {
		return nil, status, nil
	}

	bundleDeployments, err := h.bundleDeployment.List(status.Namespace, labels.Everything())
	if err != nil {
		return nil, status, err
	}

	status.Namespace = name.SafeConcatName("cluster", cluster.Namespace, cluster.Name)
	status.Summary = fleet.BundleSummary{}

	sort.Slice(bundleDeployments, func(i, j int) bool {
		return bundleDeployments[i].Name < bundleDeployments[j].Name
	})

	for _, app := range bundleDeployments {
		state := summary.GetDeploymentState(app)
		summary.IncrementState(&status.Summary, app.Name, state, summary.MessageFromDeployment(app))
		status.Summary.DesiredReady++
	}

	summary.SetReadyConditions(&status, "Bundle", status.Summary)
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

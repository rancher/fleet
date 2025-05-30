// Package cluster provides controllers for managing clusters: status changes, importing, bootstrapping.
package cluster

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/manageagent"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v3/pkg/kv"
	"github.com/rancher/wrangler/v3/pkg/relatedresource"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	clusters             fleetcontrollers.ClusterController
	clusterCache         fleetcontrollers.ClusterCache
	clusterGroups        fleetcontrollers.ClusterGroupCache
	bundleDeployment     fleetcontrollers.BundleDeploymentCache
	namespaceCache       corecontrollers.NamespaceCache
	namespaces           corecontrollers.NamespaceController
	gitRepos             fleetcontrollers.GitRepoCache
	clusterRegistrations fleetcontrollers.ClusterRegistrationController
}

func Register(ctx context.Context,
	bundleDeployment fleetcontrollers.BundleDeploymentController,
	clusterGroups fleetcontrollers.ClusterGroupCache,
	clusters fleetcontrollers.ClusterController,
	gitRepos fleetcontrollers.GitRepoCache,
	namespaces corecontrollers.NamespaceController,
	clusterRegistrations fleetcontrollers.ClusterRegistrationController) {

	h := &handler{
		clusterGroups:        clusterGroups,
		clusterCache:         clusters.Cache(),
		clusters:             clusters,
		bundleDeployment:     bundleDeployment.Cache(),
		namespaceCache:       namespaces.Cache(),
		namespaces:           namespaces,
		gitRepos:             gitRepos,
		clusterRegistrations: clusterRegistrations,
	}

	clusters.OnChange(ctx, "managed-cluster-trigger", h.ensureNSDeleted)
	fleetcontrollers.RegisterClusterStatusHandler(ctx,
		clusters,
		"Processed",
		"managed-cluster",
		h.OnClusterChanged)

	// enqueue cluster event for bundledeployment changes
	relatedresource.Watch(ctx, "managed-cluster", h.findClusters(namespaces.Cache()), clusters, bundleDeployment)
}

// ensureNSDeleted is a handler that enqueues the cluster registration namespace, when a cluster is deleted.
func (h *handler) ensureNSDeleted(key string, obj *fleet.Cluster) (*fleet.Cluster, error) {
	if obj == nil {
		logrus.Debugf("Cluster %s deleted, enqueue cluster namespace deletion", key)
		h.namespaces.Enqueue(clusterNamespace(kv.Split(key, "/")))
	}
	return obj, nil
}

// findClusters enqueues the cluster when the bundledeployment changes. It uses the cluster namespace to determine the cluster.
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
		logrus.Debugf("Enqueueing cluster %s/%s for bundledeployment %s/%s", clusterNS, clusterName, namespace, obj.(*fleet.BundleDeployment).Name)
		return []relatedresource.Key{
			{
				Namespace: clusterNS,
				Name:      clusterName,
			},
		}, nil
	}
}

// clusterNamespace returns the cluster namespace name
// for a given cluster name, e.g.:
// "cluster-fleet-local-cluster-294db1acfa77-d9ccf852678f"
func clusterNamespace(clusterNamespace, clusterName string) string {
	return names.SafeConcatName("cluster",
		clusterNamespace,
		clusterName,
		names.KeyHash(clusterNamespace+"::"+clusterName))
}

// OnClusterChanged is a handler that creates the clusters namespace
func (h *handler) OnClusterChanged(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, error) {
	if manageagent.SkipCluster(cluster) {
		return status, nil
	}

	logrus.Debugf("OnClusterChanged for cluster status %s, updating namespace in status", cluster.Name)
	if status.Namespace == "" {
		status.Namespace = clusterNamespace(cluster.Namespace, cluster.Name)
	}
	return status, h.createNamespace(cluster, status)
}

func (h *handler) createNamespace(cluster *fleet.Cluster, status fleet.ClusterStatus) error {
	_, err := h.namespaceCache.Get(status.Namespace)
	if apierrors.IsNotFound(err) {
		_, err = h.namespaces.Create(&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: status.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
				Annotations: map[string]string{
					fleet.ClusterNamespaceAnnotation: cluster.Namespace,
					fleet.ClusterAnnotation:          cluster.Name,
				},
			},
		})
	}

	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

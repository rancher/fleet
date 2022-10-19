// Package cluster provides controllers for managing clusters: status changes, importing, bootstrapping. (fleetcontroller)
package cluster

import (
	"context"
	"sort"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/controllers/clusterregistration"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"

	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kv"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

type repoKey struct {
	repo string
	ns   string
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

	relatedresource.Watch(ctx, "managed-cluster", h.findClusters(namespaces.Cache()), clusters, bundleDeployment)
}

func (h *handler) ensureNSDeleted(key string, obj *fleet.Cluster) (*fleet.Cluster, error) {
	if obj == nil {
		logrus.Debugf("Cluster %s deleted, enqueue cluster namespace deletion", key)
		h.namespaces.Enqueue(clusterToNamespace(kv.Split(key, "/")))
	}
	return obj, nil
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
		logrus.Debugf("Enqueueing cluster %s/%s for bundledeployment %s/%s", clusterNS, clusterName, namespace, obj.(*fleet.BundleDeployment).Name)
		return []relatedresource.Key{
			{
				Namespace: clusterNS,
				Name:      clusterName,
			},
		}, nil
	}
}

// clusterToNamespace returns the namespace name for a given cluster name, e.g.:
// cluster-fleet-local-cluster-294db1acfa77-d9ccf852678f
func clusterToNamespace(clusterNamespace, clusterName string) string {
	return name.SafeConcatName("cluster",
		clusterNamespace,
		clusterName,
		clusterregistration.KeyHash(clusterNamespace+"::"+clusterName))
}

func (h *handler) OnClusterChanged(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, error) {
	logrus.Debugf("OnClusterChanged for cluster status %s, checking cluster registration, updating status from bundledeployments, gitrepos", cluster.Name)
	if cluster.DeletionTimestamp != nil {
		clusterRegistrations, err := h.clusterRegistrations.List(cluster.Namespace, metav1.ListOptions{})
		if err != nil {
			return status, err
		}
		for _, clusterRegistration := range clusterRegistrations.Items {
			if clusterRegistration.Status.ClusterName == cluster.Name {
				err := h.clusterRegistrations.Delete(clusterRegistration.Namespace, clusterRegistration.Name, &metav1.DeleteOptions{})
				if err == nil {
					logrus.Debugf("deleted leftover ClusterRegistration (%s) for cluster: %s", clusterRegistration.Name, cluster.Name)
				} else if !apierrors.IsNotFound(err) {
					return status, err
				}
			}
		}
		return status, nil
	}

	if status.Namespace == "" {
		status.Namespace = clusterToNamespace(cluster.Namespace, cluster.Name)
	}

	bundleDeployments, err := h.bundleDeployment.List(status.Namespace, labels.Everything())
	if err != nil {
		return status, err
	}

	status.DesiredReadyGitRepos = 0
	status.ReadyGitRepos = 0
	status.ResourceCounts = fleet.GitRepoResourceCounts{}
	status.Summary = fleet.BundleSummary{}

	sort.Slice(bundleDeployments, func(i, j int) bool {
		return bundleDeployments[i].Name < bundleDeployments[j].Name
	})

	repos := map[repoKey]bool{}
	for _, app := range bundleDeployments {
		state := summary.GetDeploymentState(app)
		summary.IncrementState(&status.Summary, app.Name, state, summary.MessageFromDeployment(app), app.Status.ModifiedStatus, app.Status.NonReadyStatus)
		status.Summary.DesiredReady++

		repo := app.Labels[fleet.RepoLabel]
		ns := app.Labels[fleet.BundleNamespaceLabel]
		if repo != "" && ns != "" {
			repos[repoKey{repo: repo, ns: ns}] = (state == fleet.Ready) || repos[repoKey{repo: repo, ns: ns}]
		}
	}

	allReady := true
	for repo, ready := range repos {
		gitrepo, err := h.gitRepos.Get(repo.ns, repo.repo)
		if err == nil {
			summary.IncrementResourceCounts(&status.ResourceCounts, gitrepo.Status.ResourceCounts)
			status.DesiredReadyGitRepos++
			if ready {
				status.ReadyGitRepos++
			} else {
				allReady = false
			}
		}
	}

	if allReady && status.ResourceCounts.Ready != status.ResourceCounts.DesiredReady {
		logrus.Debugf("Cluster %s/%s is not ready because not all gitrepos are ready: %d/%d, enqueue cluster again",
			cluster.Namespace, cluster.Name, status.ResourceCounts.Ready, status.ResourceCounts.DesiredReady)

		// Counts from gitrepo are out of sync with bundleDeployment state
		// just retry in 15 seconds as there no great way to trigger an event that
		// doesn't cause a loop
		h.clusters.EnqueueAfter(cluster.Namespace, cluster.Name, durations.ClusterEnqueueDelay)
	}

	summary.SetReadyConditions(&status, "Bundle", status.Summary)
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

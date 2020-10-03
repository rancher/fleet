package cluster

import (
	"context"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/controllers/clusterregistration"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	"github.com/rancher/wrangler/pkg/condition"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	clusters         fleetcontrollers.ClusterCache
	clusterGroups    fleetcontrollers.ClusterGroupCache
	bundleDeployment fleetcontrollers.BundleDeploymentCache
	namespaceCache   corecontrollers.NamespaceCache
	namespaces       corecontrollers.NamespaceClient
	gitRepos         fleetcontrollers.GitRepoCache
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
	namespaces corecontrollers.NamespaceController) {

	h := &handler{
		clusterGroups:    clusterGroups,
		clusters:         clusters.Cache(),
		bundleDeployment: bundleDeployment.Cache(),
		namespaceCache:   namespaces.Cache(),
		namespaces:       namespaces,
		gitRepos:         gitRepos,
	}

	fleetcontrollers.RegisterClusterStatusHandler(ctx,
		clusters,
		"Processed",
		"managed-cluster",
		h.OnClusterChanged)

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

func (h *handler) OnClusterChanged(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, error) {
	if cluster.DeletionTimestamp != nil {
		return status, nil
	}

	if status.Namespace == "" {
		ns := name.SafeConcatName("cluster",
			cluster.Namespace,
			cluster.Name,
			clusterregistration.KeyHash(cluster.Namespace+"::"+cluster.Name))
		status.Namespace = ns
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

	repos := map[repoKey]struct{}{}
	for _, app := range bundleDeployments {
		state := summary.GetDeploymentState(app)
		summary.IncrementState(&status.Summary, app.Name, state, summary.MessageFromDeployment(app), app.Status.ModifiedStatus, app.Status.NonReadyStatus)
		status.Summary.DesiredReady++

		repo := app.Labels[fleet.RepoLabel]
		ns := app.Labels[fleet.BundleNamespaceLabel]
		if repo != "" && ns != "" {
			repos[repoKey{repo: repo, ns: ns}] = struct{}{}
		}
	}

	for repo := range repos {
		gitrepo, err := h.gitRepos.Get(repo.ns, repo.repo)
		if err == nil {
			summary.IncrementResourceCounts(&status.ResourceCounts, gitrepo.Status.ResourceCounts)
			status.DesiredReadyGitRepos++
			if condition.Cond("Ready").IsTrue(gitrepo) {
				status.ReadyGitRepos++
			}
		}
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

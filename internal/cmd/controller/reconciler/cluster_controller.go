// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/sharding"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var LongRetry = wait.Backoff{
	Steps:    5,
	Duration: 5 * time.Second,
	Factor:   1.0,
	Jitter:   0.1,
}

type repoKey struct {
	repo string
	ns   string
}

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Query   BundleQuery
	ShardID string
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("cluster")

	cluster := &fleet.Cluster{}
	err := r.Get(ctx, req.NamespacedName, cluster)
	if apierrors.IsNotFound(err) {
		metrics.ClusterCollector.Delete(req.Name, req.Namespace)
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if cluster.Status.Namespace == "" {
		// wait for the cluster's namespace to be created by agentmanagement
		return ctrl.Result{
			RequeueAfter: durations.ClusterRegisterDelay,
		}, nil
	}

	// increased log level, this triggers a lot
	logger.V(4).Info("Reconciling cluster, cleaning old bundledeployments and updating status", "oldDisplay", cluster.Status.Display)

	bundleDeployments := &fleet.BundleDeploymentList{}
	err = r.List(ctx, bundleDeployments, client.InNamespace(cluster.Status.Namespace))
	if err != nil {
		return ctrl.Result{}, err
	}

	_, cleanup, err := r.Query.BundlesForCluster(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, bundle := range cleanup {
		for _, bundleDeployment := range bundleDeployments.Items {
			if bundleDeployment.Labels[fleet.BundleLabel] == bundle.Name &&
				bundleDeployment.Labels[fleet.BundleNamespaceLabel] == bundle.Namespace {
				logger.V(1).Info("cleaning up bundleDeployment not matching the cluster", "bundledeployment", bundleDeployment)
				err := r.Delete(ctx, &bundleDeployment) // nolint:gosec // does not store pointer
				if err != nil {
					logger.V(1).Error(err, "deleting bundleDeployment returned an error")
				}
			}
		}
	}

	cluster.Status.DesiredReadyGitRepos = 0
	cluster.Status.ReadyGitRepos = 0
	cluster.Status.ResourceCounts = fleet.GitRepoResourceCounts{}
	cluster.Status.Summary = fleet.BundleSummary{}

	sort.Slice(bundleDeployments.Items, func(i, j int) bool {
		return bundleDeployments.Items[i].Name < bundleDeployments.Items[j].Name
	})

	repos := map[repoKey]bool{}
	for _, bd := range bundleDeployments.Items {
		bd := bd
		state := summary.GetDeploymentState(&bd)
		summary.IncrementState(&cluster.Status.Summary, bd.Name, state, summary.MessageFromDeployment(&bd), bd.Status.ModifiedStatus, bd.Status.NonReadyStatus)
		cluster.Status.Summary.DesiredReady++

		repo := bd.Labels[fleet.RepoLabel]
		ns := bd.Labels[fleet.BundleNamespaceLabel]
		if repo != "" && ns != "" {
			repos[repoKey{repo: repo, ns: ns}] = (state == fleet.Ready) || repos[repoKey{repo: repo, ns: ns}]
		}
	}

	allReady := true
	for repo, ready := range repos {
		gitrepo := &fleet.GitRepo{}
		err := r.Get(ctx, types.NamespacedName{Namespace: repo.ns, Name: repo.repo}, gitrepo)
		if err == nil {
			summary.IncrementResourceCounts(&cluster.Status.ResourceCounts, gitrepo.Status.ResourceCounts)
			cluster.Status.DesiredReadyGitRepos++
			if ready {
				cluster.Status.ReadyGitRepos++
			} else {
				allReady = false
			}
		}
	}

	summary.SetReadyConditions(&cluster.Status, "Bundle", cluster.Status.Summary)

	// Update display status
	cluster.Status.Display.ReadyBundles = fmt.Sprintf("%d/%d",
		cluster.Status.Summary.Ready,
		cluster.Status.Summary.DesiredReady)

	var state fleet.BundleState
	for _, nonReady := range cluster.Status.Summary.NonReadyResources {
		if fleet.StateRank[nonReady.State] > fleet.StateRank[state] {
			state = nonReady.State
		}
	}

	cluster.Status.Display.State = string(state)
	if cluster.Status.Agent.LastSeen.IsZero() {
		cluster.Status.Display.State = "WaitCheckIn"
	}

	err = retry.RetryOnConflict(LongRetry, func() error {
		t := &fleet.Cluster{}
		err := r.Get(ctx, req.NamespacedName, t)
		if err != nil {
			return err
		}
		t.Status = cluster.Status
		return r.Status().Update(ctx, t)
	})
	if err != nil {
		logger.V(1).Error(err, "Reconcile failed final update to cluster status", "status", cluster.Status)
	} else {
		metrics.ClusterCollector.Collect(ctx, cluster)
	}

	if allReady && cluster.Status.ResourceCounts.Ready != cluster.Status.ResourceCounts.DesiredReady {
		logger.V(1).Info("Cluster is not ready, because not all gitrepos are ready",
			"namespace", cluster.Namespace,
			"name", cluster.Name,
			"ready", cluster.Status.ResourceCounts.Ready,
			"desiredReady", cluster.Status.ResourceCounts.DesiredReady,
		)

		// Counts from gitrepo are out of sync with bundleDeployment state, retry in a number of seconds.
		return ctrl.Result{
			RequeueAfter: durations.ClusterRegisterDelay,
		}, nil
	}

	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Cluster{}).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		// Note: Maybe we can tune events after cleanup code is
		// removed? This relies on bundledeployments and gitrepos to
		// update its status. It also needs to trigger on
		// cluster.Status.Namespace to create the namespace.
		Complete(r)
}

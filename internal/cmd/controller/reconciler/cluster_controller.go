// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/metrics"
	"github.com/rancher/fleet/internal/resourcestatus"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/sharding"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/wrangler/v3/pkg/condition"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var LongRetry = wait.Backoff{
	Steps:    5,
	Duration: 5 * time.Second,
	Factor:   1.0,
	Jitter:   0.1,
}

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Query   BundleQuery
	ShardID string

	Workers int
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Cluster{}).
		// Watch bundledeployments so we can update the status fields
		Watches(
			&fleet.BundleDeployment{},
			handler.EnqueueRequestsFromMapFunc(r.mapBundleDeploymentToCluster),
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return true
				},
				// Triggering on every update would run into an
				// endless loop with the agentmanagement
				// cluster controller.
				// We still need to update often enough to keep the
				// status fields up to date.
				UpdateFunc: func(e event.UpdateEvent) bool {
					n := e.ObjectNew.(*fleet.BundleDeployment)
					o := e.ObjectOld.(*fleet.BundleDeployment)
					if n == nil || o == nil {
						return false
					}
					if !reflect.DeepEqual(n.Spec, o.Spec) {
						return true
					}
					if n.Status.AppliedDeploymentID != o.Status.AppliedDeploymentID {
						return true
					}
					if n.Status.Ready != o.Status.Ready {
						return true
					}
					return false
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					o := e.Object.(*fleet.BundleDeployment)
					if o == nil || o.Status.AppliedDeploymentID == "" {
						return false
					}
					return true
				},
			}),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

func indexByNamespacedName[T metav1.Object](list []T) map[types.NamespacedName]T {
	res := make(map[types.NamespacedName]T, len(list))
	for _, obj := range list {
		res[types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}] = obj
	}
	return res
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
		return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, cluster.Status, err)
	}

	// Clean up old bundledeployments
	_, cleanup, err := r.Query.BundlesForCluster(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, cluster.Status, err)
	}
	toDeleteBundles := indexByNamespacedName(cleanup)

	// Delete BundleDeployments for Bundles being removed while getting a filtered items list
	bundleDeployments.Items = slices.DeleteFunc(bundleDeployments.Items, func(bd fleet.BundleDeployment) bool {
		bundleNamespace := bd.Labels[fleet.BundleNamespaceLabel]
		bundleName := bd.Labels[fleet.BundleLabel]
		if _, ok := toDeleteBundles[types.NamespacedName{Namespace: bundleNamespace, Name: bundleName}]; ok {
			logger.V(1).Info("cleaning up bundleDeployment not matching the cluster", "bundledeployment", bd)
			if err := r.Delete(ctx, &bd); err != nil {
				logger.V(1).Info("deleting bundleDeployment returned an error", "error", err)
			}
			return true
		}
		return false
	})

	// Count the number of gitrepo, bundledeployemt and deployed resources for this cluster
	cluster.Status.DesiredReadyGitRepos = 0
	cluster.Status.ReadyGitRepos = 0
	cluster.Status.ResourceCounts = fleet.ResourceCounts{}
	cluster.Status.Summary = fleet.BundleSummary{}

	sort.Slice(bundleDeployments.Items, func(i, j int) bool {
		return bundleDeployments.Items[i].Name < bundleDeployments.Items[j].Name
	})

	resourcestatus.SetClusterResources(bundleDeployments, cluster)

	repos := map[types.NamespacedName]bool{}
	for _, bd := range bundleDeployments.Items {
		state := summary.GetDeploymentState(&bd)
		summary.IncrementState(&cluster.Status.Summary, bd.Name, state, summary.MessageFromDeployment(&bd), bd.Status.ModifiedStatus, bd.Status.NonReadyStatus)
		cluster.Status.Summary.DesiredReady++

		repoNamespace, repoName := bd.Labels[fleet.BundleNamespaceLabel], bd.Labels[fleet.RepoLabel]
		if repoNamespace != "" && repoName != "" {
			// a gitrepo is ready if its bundledeployments are ready, take previous state into account
			repoKey := types.NamespacedName{Namespace: repoNamespace, Name: repoName}
			repos[repoKey] = (state == fleet.Ready) || repos[repoKey]
		}
	}

	// a cluster is ready if all its gitrepos are ready and the resources are ready too
	allReady := true
	for repo, ready := range repos {
		gitrepo := &fleet.GitRepo{}
		if err := r.Get(ctx, repo, gitrepo); err == nil {
			cluster.Status.DesiredReadyGitRepos++
			if ready {
				cluster.Status.ReadyGitRepos++
			} else {
				allReady = false
			}
		}
	}

	summary.SetReadyConditions(&cluster.Status, "Bundle", cluster.Status.Summary)

	// Calculate display status fields
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

	r.setCondition(&cluster.Status, nil)

	err = r.updateStatus(ctx, req.NamespacedName, cluster.Status)
	if err != nil {
		logger.V(1).Info("Reconcile failed final update to cluster status", "status", cluster.Status, "error", err)
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

// setCondition sets the condition and updates the timestamp, if the condition changed
func (r *ClusterReconciler) setCondition(status *fleet.ClusterStatus, err error) {
	cond := condition.Cond(fleet.ClusterConditionProcessed)
	origStatus := status.DeepCopy()
	cond.SetError(status, "", fleetutil.IgnoreConflict(err))
	if !equality.Semantic.DeepEqual(origStatus, status) {
		cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
	}
}

func (r *ClusterReconciler) updateErrorStatus(ctx context.Context, req types.NamespacedName, status fleet.ClusterStatus, orgErr error) error {
	r.setCondition(&status, orgErr)
	if statusErr := r.updateStatus(ctx, req, status); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}
	return orgErr
}

func (r *ClusterReconciler) updateStatus(ctx context.Context, req types.NamespacedName, status fleet.ClusterStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.Cluster{}
		err := r.Get(ctx, req, t)
		if err != nil {
			return err
		}
		t.Status = status
		return r.Status().Update(ctx, t)
	})
}

func (r *ClusterReconciler) mapBundleDeploymentToCluster(ctx context.Context, a client.Object) []ctrl.Request {
	clusterNS := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: a.GetNamespace()}, clusterNS)
	if err != nil {
		return nil
	}

	ns := clusterNS.Annotations[fleet.ClusterNamespaceAnnotation]
	name := clusterNS.Annotations[fleet.ClusterAnnotation]
	if ns == "" || name == "" {
		return nil
	}

	log.FromContext(ctx).WithName("cluster-handler").V(1).Info("Enqueueing cluster for bundledeployment",
		"cluster", name,
		"bundledeployment", a.(*fleet.BundleDeployment).Name,
		"clusterNamespace", clusterNS.Name,
		"clusterRegistrationNamespace", ns)

	return []ctrl.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: ns,
			Name:      name,
		},
	}}
}

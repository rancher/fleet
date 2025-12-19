package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/manageagent"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// clusterReconciler implements a simple reconcile loop for Cluster objects that
// mirrors the original wrangler-based OnClusterChanged handler plus deletion
// behaviour (remove cluster namespace when cluster deleted).
type clusterReconciler struct {
	client client.Client
}

func (r *clusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Cluster{}).
		Complete(r)
}

func (r *clusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster fleet.Cluster
	if err := r.client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if errors.IsNotFound(err) {
			// cluster deleted: remove cluster namespace
			nsName := clusterNamespace(req.NamespacedName.Namespace, req.NamespacedName.Name)
			var ns v1.Namespace
			if err := r.client.Get(ctx, client.ObjectKey{Name: nsName}, &ns); err == nil {
				_ = r.client.Delete(ctx, &ns)
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if manageagent.SkipCluster(&cluster) {
		return ctrl.Result{}, nil
	}

	// set status.Namespace (use controller-runtime client to update if needed)
	if cluster.Status.Namespace == "" {
		cluster.Status.Namespace = clusterNamespace(cluster.Namespace, cluster.Name)
		if err := r.client.Status().Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	// ensure namespace exists
	var ns v1.Namespace
	if err := r.client.Get(ctx, client.ObjectKey{Name: cluster.Status.Namespace}, &ns); err != nil {
		if errors.IsNotFound(err) {
			ns = v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   cluster.Status.Namespace,
					Labels: map[string]string{fleet.ManagedLabel: "true"},
					Annotations: map[string]string{
						fleet.ClusterNamespaceAnnotation: cluster.Namespace,
						fleet.ClusterAnnotation:          cluster.Name,
					},
				},
			}
			if err := r.client.Create(ctx, &ns); err != nil && !errors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
		} else {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// bundleDeploymentEnqueuer watches BundleDeployment objects and triggers the
// Cluster reconcile by patching an annotation on the Cluster resource.
type bundleDeploymentEnqueuer struct {
	client client.Client
}

func (b *bundleDeploymentEnqueuer) SetupWithManager(mgr ctrl.Manager) error {
	b.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.BundleDeployment{}).
		Complete(b)
}

func (b *bundleDeploymentEnqueuer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var bd fleet.BundleDeployment
	if err := b.client.Get(ctx, req.NamespacedName, &bd); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	nsName := bd.GetNamespace()
	var nsObj v1.Namespace
	if err := b.client.Get(ctx, client.ObjectKey{Name: nsName}, &nsObj); err != nil {
		return ctrl.Result{}, nil
	}

	clusterNS := nsObj.Annotations[fleet.ClusterNamespaceAnnotation]
	clusterName := nsObj.Annotations[fleet.ClusterAnnotation]
	if clusterNS == "" || clusterName == "" {
		return ctrl.Result{}, nil
	}

	var cluster fleet.Cluster
	if err := b.client.Get(ctx, client.ObjectKey{Namespace: clusterNS, Name: clusterName}, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations["fleet.cattle.io/bd-trigger"] = fmt.Sprintf("%d", time.Now().UnixNano())

	patch := client.MergeFrom(cluster.DeepCopy())
	if err := b.client.Patch(ctx, &cluster, patch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

package cleanup

import (
	"context"
	"time"

	"github.com/jpillora/backoff"

	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Options struct {
	Min    time.Duration
	Max    time.Duration
	Factor float64
}

func ClusterRegistrations(ctx context.Context, cl client.Client, opts Options) error {
	logger := log.FromContext(ctx)

	// lookup for all existing clusters
	seen := map[types.NamespacedName]struct{}{}
	clusterList := &fleetv1.ClusterList{}
	_ = cl.List(ctx, clusterList)
	for _, c := range clusterList.Items {
		clusterKey := types.NamespacedName{Namespace: c.Namespace, Name: c.Name}
		seen[clusterKey] = struct{}{}
	}

	crList := &fleetv1.ClusterRegistrationList{}
	_ = cl.List(ctx, crList)

	logger.Info("Listing resources", "clusters", len(clusterList.Items), "clusterRegistrations", len(crList.Items))

	// figure out the latest granted registration request per cluster
	latestGranted := map[types.NamespacedName]metav1.Time{}
	for _, cr := range crList.Items {
		if cr.Status.ClusterName == "" {
			continue
		}

		logger.Info("Mapping cluster registration")
		clusterKey := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Status.ClusterName}
		ts := latestGranted[clusterKey]
		if cr.Status.Granted && ts.Before(&cr.CreationTimestamp) {
			latestGranted[clusterKey] = cr.CreationTimestamp
		}
	}

	// sleep to not overload the API server
	b := backoff.Backoff{
		Min:    opts.Min,
		Max:    opts.Max,
		Factor: opts.Factor,
		Jitter: true,
	}

	// it should be safe to delete up to, but not including, the latest
	// granted registration
	// * requests after that might be in flight
	// * requests before that are outdated
	for _, cr := range crList.Items {
		logger := logger.WithValues("namespace", cr.Namespace, "name", cr.Name)
		logger.Info("Inspect cluster registration")
		if cr.Status.ClusterName == "" {
			continue
		}

		clusterKey := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Status.ClusterName}
		latest, found := latestGranted[clusterKey]
		if found && cr.CreationTimestamp.Before(&latest) {
			t := b.Duration()
			logger.Info("Deleting outdated, granted cluster registration, waiting", "duration", t)
			time.Sleep(t)
			if err := cl.Delete(ctx, &cr); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete clusterregistration")
			}
			// also try to delete orphan resources
			_ = cl.Delete(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: cr.Namespace, Name: cr.Name}})
			_ = cl.Delete(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: cr.Namespace, Name: cr.Name}})

			saList := &corev1.ServiceAccountList{}
			_ = cl.List(ctx, saList, client.MatchingLabels{
				"fleet.cattle.io/cluster-registration":           cr.Name,
				"fleet.cattle.io/cluster-registration-namespace": cr.Namespace})
			for _, sa := range saList.Items {
				_ = cl.Delete(ctx, &sa)
			}

			owner := client.MatchingLabels{
				"objectset.rio.cattle.io/owner-name":      cr.Name,
				"objectset.rio.cattle.io/owner-namespace": cr.Namespace,
			}
			rbList := &rbacv1.RoleBindingList{}
			_ = cl.List(ctx, rbList, owner)
			for _, rb := range rbList.Items {
				_ = cl.Delete(ctx, &rb)
			}

			crbList := &rbacv1.ClusterRoleBindingList{}
			_ = cl.List(ctx, crbList, owner)
			for _, crb := range crbList.Items {
				_ = cl.Delete(ctx, &crb)
			}
		}
	}

	// delete cluster registrations that have no cluster
	for _, cr := range crList.Items {
		if cr.Status.ClusterName == "" {
			continue
		}

		clusterKey := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Status.ClusterName}
		if _, found := seen[clusterKey]; !found {
			logger := logger.WithValues("namespace", cr.Namespace, "name", cr.Name)
			logger.Info("Deleting granted cluster registration without cluster")
			if err := cl.Delete(ctx, &cr); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete cluster registration")
			}
			continue
		}
	}

	return nil
}

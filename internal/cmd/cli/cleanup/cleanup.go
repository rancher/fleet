package cleanup

import (
	"context"
	"time"

	"github.com/jpillora/backoff"
	"github.com/rancher/fleet/internal/client"
	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

type Getter interface {
	Get() (*client.Client, error)
	GetNamespace() string
}

type Options struct {
	Min    time.Duration
	Max    time.Duration
	Factor float64
}

func ClusterRegistrations(ctx context.Context, client Getter, opts Options) error {
	c, err := client.Get()
	if err != nil {
		return err
	}

	// get the clients
	cluster := c.Fleet.Cluster()
	clusterRegistration := c.Fleet.ClusterRegistration()
	serviceAccount := c.Core.ServiceAccount()
	role := c.RBAC.Role()
	roleBinding := c.RBAC.RoleBinding()
	clusterRoleBinding := c.RBAC.ClusterRoleBinding()

	// lookup for all existing clusters
	seen := map[types.NamespacedName]struct{}{}
	clusterList, _ := cluster.List("", metav1.ListOptions{})
	for _, c := range clusterList.Items {
		clusterKey := types.NamespacedName{Namespace: c.Namespace, Name: c.Name}
		seen[clusterKey] = struct{}{}
	}

	crList, _ := clusterRegistration.List("", metav1.ListOptions{})

	logrus.Infof("Found %d clusters and %d cluster registrations", len(clusterList.Items), len(crList.Items))

	// figure out the latest granted registration request per cluster
	latestGranted := map[types.NamespacedName]metav1.Time{}
	for _, cr := range crList.Items {
		if cr.Status.ClusterName == "" {
			continue
		}

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
		logrus.Debugf("Inspect cluster registration %s/%s", cr.Namespace, cr.Name)
		if cr.Status.ClusterName == "" {
			continue
		}

		clusterKey := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Status.ClusterName}
		latest, found := latestGranted[clusterKey]
		if found && cr.CreationTimestamp.Before(&latest) {
			t := b.Duration()
			logrus.Infof("Deleting outdated, granted cluster registration %s/%s, wait for %s", cr.Namespace, cr.Name, t)
			time.Sleep(t)
			if err := clusterRegistration.Delete(cr.Namespace, cr.Name, nil); err != nil && !apierrors.IsNotFound(err) {
				logrus.Errorf("Failed to delete clusterregistration %s/%s: %v", cr.Namespace, cr.Name, err)
			}
			// also try to delete orphan resources
			_ = role.Delete(cr.Namespace, cr.Name, nil)
			_ = roleBinding.Delete(cr.Namespace, cr.Name, nil)

			filter := labels.Set(map[string]string{"fleet.cattle.io/cluster-registration": cr.Name, "fleet.cattle.io/cluster-registration-namespace": cr.Namespace}).AsSelector().String()
			saList, _ := serviceAccount.List("", metav1.ListOptions{LabelSelector: filter})
			for _, sa := range saList.Items {
				_ = serviceAccount.Delete(sa.Namespace, sa.Name, nil)
			}

			owner := labels.Set(map[string]string{"objectset.rio.cattle.io/owner-name": cr.Name, "objectset.rio.cattle.io/owner-namespace": cr.Namespace}).AsSelector().String()
			rbList, _ := roleBinding.List("", metav1.ListOptions{LabelSelector: owner})
			for _, rb := range rbList.Items {
				_ = roleBinding.Delete(rb.Namespace, rb.Name, nil)
			}

			crbList, _ := clusterRoleBinding.List(metav1.ListOptions{LabelSelector: owner})
			for _, crb := range crbList.Items {
				_ = clusterRoleBinding.Delete(crb.Name, nil)
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
			logrus.Infof("Deleting granted cluster registration without cluster %s/%s", cr.Namespace, cr.Name)
			if err := clusterRegistration.Delete(cr.Namespace, cr.Name, nil); err != nil && !apierrors.IsNotFound(err) {
				logrus.Errorf("Failed to delete cluster registration %s/%s: %v", cr.Namespace, cr.Name, err)
			}
			continue
		}
	}

	return nil
}

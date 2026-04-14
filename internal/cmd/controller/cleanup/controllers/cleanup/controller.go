// Package cleanup provides a controller that cleans up resources that are no longer needed.
package cleanup

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/sirupsen/logrus"

	"github.com/rancher/wrangler/v3/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	rbaccontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	apply          apply.Apply
	clusters       fleetcontrollers.ClusterCache
	clustersClient fleetcontrollers.ClusterClient
	namespaces     corecontrollers.NamespaceClient
}

func Register(ctx context.Context, apply apply.Apply,
	secrets corecontrollers.SecretController,
	serviceAccount corecontrollers.ServiceAccountController,
	role rbaccontrollers.RoleController,
	roleBinding rbaccontrollers.RoleBindingController,
	clusterRole rbaccontrollers.ClusterRoleController,
	clusterRoleBinding rbaccontrollers.ClusterRoleBindingController,
	namespaces corecontrollers.NamespaceController,
	clusterCache fleetcontrollers.ClusterCache,
	clusterClient fleetcontrollers.ClusterClient) {
	h := &handler{
		apply:          apply,
		clusters:       clusterCache,
		clustersClient: clusterClient,
		namespaces:     namespaces,
	}

	clusterRole.OnChange(ctx, "managed-cleanup", func(_ string, obj *rbacv1.ClusterRole) (*rbacv1.ClusterRole, error) {
		if obj == nil {
			return nil, nil
		}
		return obj, h.cleanup(obj)
	})

	clusterRoleBinding.OnChange(ctx, "managed-cleanup", func(_ string, obj *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error) {
		if obj == nil {
			return nil, nil
		}
		return obj, h.cleanup(obj)
	})

	role.OnChange(ctx, "managed-cleanup", func(_ string, obj *rbacv1.Role) (*rbacv1.Role, error) {
		if obj == nil {
			return nil, nil
		}
		return obj, h.cleanup(obj)
	})

	roleBinding.OnChange(ctx, "managed-cleanup", func(_ string, obj *rbacv1.RoleBinding) (*rbacv1.RoleBinding, error) {
		if obj == nil {
			return nil, nil
		}
		return obj, h.cleanup(obj)
	})

	serviceAccount.OnChange(ctx, "managed-cleanup", func(_ string, obj *corev1.ServiceAccount) (*corev1.ServiceAccount, error) {
		if obj == nil {
			return nil, nil
		}
		return obj, h.cleanup(obj)
	})

	secrets.OnChange(ctx, "managed-cleanup", func(_ string, obj *corev1.Secret) (*corev1.Secret, error) {
		if obj == nil {
			return nil, nil
		}
		return obj, h.cleanup(obj)
	})

	namespaces.OnChange(ctx, "managed-namespace-cleanup", h.cleanupNamespace)
}

func (h *handler) cleanupNamespace(key string, obj *corev1.Namespace) (*corev1.Namespace, error) {
	if obj == nil || obj.Labels[fleet.ManagedLabel] != "true" {
		return obj, nil
	}

	clusterNS := obj.Annotations[fleet.ClusterNamespaceAnnotation]
	clusterName := obj.Annotations[fleet.ClusterAnnotation]

	// check if the cluster for this cluster namespace still exists, otherwise clean up the namespace.
	// First consult the informer cache; if the cache reports not-found, confirm with a live API call
	// to avoid a race where the Cluster was just created and hasn't been reflected in the cache yet.
	_, err := h.clusters.Get(clusterNS, clusterName)
	if !apierrors.IsNotFound(err) {
		return obj, err
	}

	// Cache said not-found — verify against the API server before deleting.
	_, err = h.clustersClient.Get(clusterNS, clusterName, metav1.GetOptions{})
	if err == nil {
		// Cluster exists in the API server; the cache is stale. Do not delete the namespace.
		return obj, nil
	}
	if !apierrors.IsNotFound(err) {
		return obj, err
	}

	logrus.Infof("Cleaning up fleet-managed namespace %q, cluster not found", obj.Name)
	return obj, h.namespaces.Delete(key, nil)
}

func (h *handler) cleanup(obj runtime.Object) error {
	meta, err := meta.Accessor(obj)
	if err != nil {
		return err
	}
	if meta.GetLabels()[fleet.ManagedLabel] != "true" {
		return nil
	}

	// If orphaned, purge the fleet-managed resource, this is often a no-op
	err = h.apply.PurgeOrphan(obj)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

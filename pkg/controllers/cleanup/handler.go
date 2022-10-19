// Package cleanup provides a controller that cleans up resources that are no longer needed. (fleetcontroller)
package cleanup

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/sirupsen/logrus"

	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	rbaccontrollers "github.com/rancher/wrangler/pkg/generated/controllers/rbac/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	apply      apply.Apply
	clusters   fleetcontrollers.ClusterCache
	namespaces corecontrollers.NamespaceClient
}

func Register(ctx context.Context, apply apply.Apply,
	secrets corecontrollers.SecretController,
	serviceAccount corecontrollers.ServiceAccountController,
	bundledeployment fleetcontrollers.BundleDeploymentController,
	role rbaccontrollers.RoleController,
	roleBinding rbaccontrollers.RoleBindingController,
	clusterRole rbaccontrollers.ClusterRoleController,
	clusterRoleBinding rbaccontrollers.ClusterRoleBindingController,
	namespaces corecontrollers.NamespaceController,
	clusterCache fleetcontrollers.ClusterCache) {
	h := &handler{
		apply:      apply,
		clusters:   clusterCache,
		namespaces: namespaces,
	}

	bundledeployment.OnChange(ctx, "managed-cleanup", func(_ string, obj *fleet.BundleDeployment) (*fleet.BundleDeployment, error) {
		if obj == nil {
			return nil, nil
		}
		return obj, h.cleanup(obj)
	})

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

	logrus.Debugf("Cleaning up fleet-managed namespace %s", obj.Name)

	_, err := h.clusters.Get(obj.Annotations[fleet.ClusterNamespaceAnnotation], obj.Annotations[fleet.ClusterAnnotation])
	if apierrors.IsNotFound(err) {
		err = h.namespaces.Delete(key, nil)
		return obj, err
	}
	return obj, err
}

func (h *handler) cleanup(ns runtime.Object) error {
	meta, err := meta.Accessor(ns)
	if err != nil {
		return err
	}
	if meta.GetLabels()[fleet.ManagedLabel] != "true" {
		return nil
	}

	logrus.Debugf("Cleaning up fleet-managed resource %s", meta.GetName())
	err = h.apply.PurgeOrphan(ns)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

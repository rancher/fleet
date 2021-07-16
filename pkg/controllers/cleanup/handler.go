package cleanup

import (
	"context"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	rbaccontrollers "github.com/rancher/wrangler/pkg/generated/controllers/rbac/v1"
	"github.com/sirupsen/logrus"
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
	if obj == nil {
		return obj, nil
	}
	logrus.Infof("[NICK|CLEANUP|NAMESPACE] LOADED: %s (%s)", obj.Name, key)
	if obj.Labels[fleet.ManagedLabel] != "true" {
		logrus.Infof("[NICK|CLEANUP|NAMESPACE] NOT MANAGED: %s (%s)", obj.Name, key)
		return obj, nil
	}
	_, err := h.clusters.Get(obj.Annotations[fleet.ClusterNamespaceAnnotation], obj.Annotations[fleet.ClusterAnnotation])
	if apierrors.IsNotFound(err) {
		logrus.Infof("[NICK|CLEANUP|NAMESPACE] DELETING: %s (%s)", obj.Name, key)
		err = h.namespaces.Delete(key, nil)
		return obj, err
	}
	if err != nil {
		logrus.Infof("[NICK|CLEANUP|NAMESPACE] ERROR: %s (%s): %v", obj.Name, key, err)
	}
	logrus.Infof("[NICK|CLEANUP|NAMESPACE] EXIT: %s (%s)", obj.Name, key)
	return obj, err
}

func (h *handler) cleanup(obj runtime.Object) error {
	meta, err := meta.Accessor(obj)
	if err != nil {
		return err
	}
	logrus.Infof("[NICK|CLEANUP] LOADED: %s", meta.GetName())
	if meta.GetLabels()[fleet.ManagedLabel] != "true" {
		logrus.Infof("[NICK|CLEANUP] NOT MANAGED: %s", meta.GetName())
		return nil
	}
	logrus.Infof("[NICK|CLEANUP] PURGING: %s", meta.GetName())
	err = h.apply.PurgeOrphan(obj)
	if apierrors.IsNotFound(err) {
		logrus.Infof("[NICK|CLEANUP] DOES NOT EXIST: %s", meta.GetName())
		return nil
	}
	logrus.Infof("[NICK|CLEANUP] EXIT: %s", meta.GetName())
	return err
}

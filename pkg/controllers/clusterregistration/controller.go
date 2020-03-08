package clusterregistration

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/fleet/pkg/controllers/sharedindex"

	rbaccontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/rbac/v1"

	fleetgroup "github.com/rancher/fleet/pkg/apis/fleet.cattle.io"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/name"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	clusterRegistration fleetcontrollers.ClusterRegistrationRequestController
	clusterCache        fleetcontrollers.ClusterCache
	clusterGroupCache   fleetcontrollers.ClusterGroupCache
	clusters            fleetcontrollers.ClusterClient
}

func Register(ctx context.Context,
	apply apply.Apply,
	serviceAccount corecontrollers.ServiceAccountController,
	role rbaccontrollers.RoleController,
	roleBinding rbaccontrollers.RoleBindingController,
	clusterRole rbaccontrollers.ClusterRoleController,
	clusterRoleBinding rbaccontrollers.ClusterRoleBindingController,
	clusterRegistration fleetcontrollers.ClusterRegistrationRequestController,
	clusterCache fleetcontrollers.ClusterCache,
	clusterGroupCache fleetcontrollers.ClusterGroupCache,
	clusters fleetcontrollers.ClusterClient) {
	h := &handler{
		clusterRegistration: clusterRegistration,
		clusterGroupCache:   clusterGroupCache,
		clusterCache:        clusterCache,
		clusters:            clusters,
	}

	// register CreateCluster handler first to ensure cluster exists before we get to the generation handler
	clusterRegistration.OnChange(ctx, "cluster-registration", h.CreateCluster)
	fleetcontrollers.RegisterClusterRegistrationRequestGeneratingHandler(ctx,
		clusterRegistration,
		apply.WithCacheTypes(serviceAccount,
			role,
			roleBinding,
			clusterRole,
			clusterRoleBinding,
			clusterRegistration,
		),
		"",
		"cluster-registration",
		h.OnChange,
		&generic.GeneratingHandlerOptions{
			AllowClusterScoped: true,
		})
}

func (h *handler) CreateCluster(key string, request *fleet.ClusterRegistrationRequest) (*fleet.ClusterRegistrationRequest, error) {
	if request == nil {
		return nil, nil
	}

	cluster, err := h.ensureClusterExists(request)
	if err != nil || cluster == nil {
		return request, err
	}

	if request.Status.ClusterName == "" {
		request = request.DeepCopy()
		request.Status.ClusterName = cluster.Name
		request.Status.ClusterNamespace = cluster.Namespace
		return h.clusterRegistration.UpdateStatus(request)
	}

	return request, nil
}

func (h *handler) OnChange(request *fleet.ClusterRegistrationRequest, status fleet.ClusterRegistrationRequestStatus) ([]runtime.Object, fleet.ClusterRegistrationRequestStatus, error) {
	cluster, err := h.ensureClusterExists(request)
	if err != nil || cluster == nil {
		return nil, status, err
	}

	if cluster.Status.Namespace == "" {
		h.clusterRegistration.EnqueueAfter(request.Namespace, request.Name, 2*time.Second)
		return nil, status, nil
	}

	status.Granted = true
	return []runtime.Object{
		&v1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: request.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
					fleet.ClusterAnnotation: cluster.Name,
					fleet.RequestAnnotation: request.Name,
				},
			},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: cluster.Status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get", "list", "watch"},
					APIGroups: []string{fleetgroup.GroupName},
					Resources: []string{fleet.BundleDeploymentResourceName},
				},
				{
					Verbs:     []string{"update"},
					APIGroups: []string{fleetgroup.GroupName},
					Resources: []string{fleet.BundleDeploymentResourceName + "/status"},
				},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: cluster.Status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      request.Name,
					Namespace: request.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     request.Name,
			},
		},
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: name.SafeConcatName(request.Namespace, request.Name),
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{fleetgroup.GroupName},
					Resources: []string{fleet.ContentResourceName},
				},
			},
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: name.SafeConcatName(request.Namespace, request.Name),
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      request.Name,
					Namespace: request.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     name.SafeConcatName(request.Namespace, request.Name),
			},
		},
	}, status, nil
}

func (h *handler) ensureClusterExists(request *fleet.ClusterRegistrationRequest) (*fleet.Cluster, error) {
	clusterGroups, err := h.clusterGroupCache.GetByIndex(sharedindex.ClusterGroupByNamespace, request.Namespace)
	if err != nil {
		return nil, err
	}
	if len(clusterGroups) == 0 {
		return nil, fmt.Errorf("failed to find cluster group for namespace %s", request.Namespace)
	}

	ns := clusterGroups[0].Status.Namespace

	clusterName := name.SafeConcatName("cluster", request.Spec.ClientID)
	if cluster, err := h.clusterCache.Get(ns, clusterName); !apierrors.IsNotFound(err) {
		return cluster, err
	}

	if request.Status.ClusterName == "" {
		return h.clusters.Create(&fleet.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: ns,
				Labels:    request.Spec.ClusterLabels,
			},
		})
	}

	return nil, h.clusterRegistration.Delete(request.Namespace, request.Name, nil)
}

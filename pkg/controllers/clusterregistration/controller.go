package clusterregistration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/rancher/fleet/pkg/registration"

	fleetgroup "github.com/rancher/fleet/pkg/apis/fleet.cattle.io"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	rbaccontrollers "github.com/rancher/wrangler/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	AgentCredentialSecretType = "fleet.cattle.io/agent-credential"
	clusterByClientID         = "clusterByClientID"
)

type handler struct {
	clusterRegistration fleetcontrollers.ClusterRegistrationController
	clusterCache        fleetcontrollers.ClusterCache
	clusters            fleetcontrollers.ClusterClient
	serviceAccountCache corecontrollers.ServiceAccountCache
	secretsCache        corecontrollers.SecretCache
	secrets             corecontrollers.SecretClient
}

func Register(ctx context.Context,
	apply apply.Apply,
	serviceAccount corecontrollers.ServiceAccountController,
	secret corecontrollers.SecretController,
	role rbaccontrollers.RoleController,
	roleBinding rbaccontrollers.RoleBindingController,
	clusterRole rbaccontrollers.ClusterRoleController,
	clusterRoleBinding rbaccontrollers.ClusterRoleBindingController,
	clusterRegistration fleetcontrollers.ClusterRegistrationController,
	clusterCache fleetcontrollers.ClusterCache,
	clusters fleetcontrollers.ClusterClient) {
	h := &handler{
		clusterRegistration: clusterRegistration,
		clusterCache:        clusterCache,
		clusters:            clusters,
		serviceAccountCache: serviceAccount.Cache(),
		secrets:             secret,
		secretsCache:        secret.Cache(),
	}

	fleetcontrollers.RegisterClusterRegistrationGeneratingHandler(ctx,
		clusterRegistration,
		apply.WithCacheTypes(serviceAccount,
			secret,
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

	clusterCache.AddIndexer(clusterByClientID, func(obj *fleet.Cluster) ([]string, error) {
		return []string{
			fmt.Sprintf("%s/%s", obj.Namespace, obj.Spec.ClientID),
		}, nil
	})
}

func (h *handler) authorizeCluster(sa *v1.ServiceAccount, cluster *fleet.Cluster, req *fleet.ClusterRegistration) (*v1.Secret, error) {
	if len(sa.Secrets) == 0 {
		return nil, nil
	}
	secret, err := h.secretsCache.Get(sa.Namespace, sa.Secrets[0].Name)
	if apierrors.IsNotFound(err) {
		// secrets can be slow to propagate to the cache
		secret, err = h.secrets.Get(sa.Namespace, sa.Secrets[0].Name, metav1.GetOptions{})
	}
	if err != nil || secret == nil {
		return nil, err
	}
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registration.SecretName(req.Spec.ClientID, req.Spec.ClientRandom),
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				fleet.ClusterAnnotation: cluster.Name,
			},
			Annotations: map[string]string{
				fleet.ManagedAnnotation: "true",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: fleet.SchemeGroupVersion.String(),
					Kind:       "Cluster",
					Name:       cluster.Name,
					UID:        cluster.UID,
				},
			},
		},
		Type: AgentCredentialSecretType,
		Data: map[string][]byte{
			"token":               secret.Data["token"],
			"deploymentNamespace": []byte(cluster.Status.Namespace),
			"clusterNamespace":    []byte(cluster.Namespace),
			"clusterName":         []byte(cluster.Name),
		},
	}, nil
}

func (h *handler) OnChange(request *fleet.ClusterRegistration, status fleet.ClusterRegistrationStatus) ([]runtime.Object, fleet.ClusterRegistrationStatus, error) {
	var (
		objects []runtime.Object
	)

	cluster, err := h.createOrGetCluster(request)
	if err != nil || cluster == nil {
		return nil, status, err
	}

	if cluster.Status.Namespace == "" {
		h.clusterRegistration.EnqueueAfter(request.Namespace, request.Name, 2*time.Second)
		return nil, status, nil
	}

	saName := name.SafeConcatName(request.Name, string(request.UID))
	sa, err := h.serviceAccountCache.Get(cluster.Status.Namespace, saName)
	if err == nil {
		if secret, err := h.authorizeCluster(sa, cluster, request); err != nil {
			return nil, status, err
		} else if secret != nil {
			status.Granted = true
			objects = append(objects, secret)
		} else {
			h.clusterRegistration.EnqueueAfter(request.Namespace, request.Name, 2*time.Second)
		}
	}

	status.ClusterName = cluster.Name
	return append(objects,
		&v1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: cluster.Status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
					fleet.ClusterAnnotation: cluster.Name,
				},
			},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: request.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:         []string{"patch"},
					APIGroups:     []string{fleetgroup.GroupName},
					Resources:     []string{fleet.ClusterResourceName + "/status"},
					ResourceNames: []string{cluster.Name},
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
					Name:      saName,
					Namespace: cluster.Status.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     request.Name,
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: request.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: cluster.Status.Namespace,
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
					Name:      saName,
					Namespace: cluster.Status.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     name.SafeConcatName(request.Namespace, request.Name),
			},
		}), status, nil
}

func KeyHash(s string) string {
	if len(s) > 100 {
		s = s[:100]
	}
	d := sha256.Sum256([]byte(s))
	return hex.EncodeToString(d[:])[:12]
}

func (h *handler) createOrGetCluster(request *fleet.ClusterRegistration) (*fleet.Cluster, error) {
	clusters, err := h.clusterCache.GetByIndex(clusterByClientID, fmt.Sprintf("%s/%s", request.Namespace, request.Spec.ClientID))
	if err == nil && len(clusters) > 0 {
		return clusters[0], nil
	} else if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	clusterName := name.SafeConcatName("cluster", KeyHash(request.Spec.ClientID))
	if cluster, err := h.clusterCache.Get(request.Namespace, clusterName); !apierrors.IsNotFound(err) {
		if cluster.Spec.ClientID != request.Spec.ClientID {
			// This would happen with a hash collision
			return nil, fmt.Errorf("non-matching ClientID on cluster %s/%s got %s expected %s",
				request.Namespace, clusterName, cluster.Spec.ClientID, request.Spec.ClientID)
		}
		return cluster, err
	}

	labels := map[string]string{}
	for k, v := range request.Spec.ClusterLabels {
		labels[k] = v
	}
	labels[fleet.ClusterAnnotation] = clusterName

	logrus.Infof("Creating cluster %s/%s", request.Namespace, clusterName)
	cluster, err := h.clusters.Create(&fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: request.Namespace,
			Labels:    labels,
		},
		Spec: fleet.ClusterSpec{
			ClientID: request.Spec.ClientID,
		},
	})
	return cluster, err
}

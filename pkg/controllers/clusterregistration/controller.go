// Package clusterregistration implements manager-initiated and agent-initiated registration. (fleetcontroller)
//
// Add or import downstream clusters / agents to Fleet and keep information
// from their registration (e.g. local cluster kubeconfig) up-to-date.
package clusterregistration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	fleetgroup "github.com/rancher/fleet/pkg/apis/fleet.cattle.io"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/registration"
	secretutil "github.com/rancher/fleet/pkg/secret"

	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	rbaccontrollers "github.com/rancher/wrangler/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"

	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	AgentCredentialSecretType     = "fleet.cattle.io/agent-credential"
	clusterByClientID             = "clusterByClientID"
	clusterRegistrationByClientID = "clusterRegistrationByClientID"
	deleteSecretAfter             = durations.ClusterRegistrationDeleteDelay
)

type handler struct {
	systemNamespace             string
	systemRegistrationNamespace string
	clusterRegistration         fleetcontrollers.ClusterRegistrationController
	clusterCache                fleetcontrollers.ClusterCache
	clusters                    fleetcontrollers.ClusterClient
	serviceAccountCache         corecontrollers.ServiceAccountCache
	secretsCache                corecontrollers.SecretCache
	secrets                     corecontrollers.SecretController
}

func Register(ctx context.Context,
	apply apply.Apply,
	systemNamespace string,
	systemRegistrationNamespace string,
	serviceAccount corecontrollers.ServiceAccountController,
	secret corecontrollers.SecretController,
	role rbaccontrollers.RoleController,
	roleBinding rbaccontrollers.RoleBindingController,
	clusterRegistration fleetcontrollers.ClusterRegistrationController,
	clusters fleetcontrollers.ClusterController) {
	h := &handler{
		systemNamespace:             systemNamespace,
		systemRegistrationNamespace: systemRegistrationNamespace,
		clusterRegistration:         clusterRegistration,
		clusterCache:                clusters.Cache(),
		clusters:                    clusters,
		serviceAccountCache:         serviceAccount.Cache(),
		secrets:                     secret,
		secretsCache:                secret.Cache(),
	}

	fleetcontrollers.RegisterClusterRegistrationGeneratingHandler(ctx,
		clusterRegistration,
		apply.WithCacheTypes(serviceAccount,
			secret,
			role,
			roleBinding,
		),
		"",
		"cluster-registration",
		h.OnChange,
		&generic.GeneratingHandlerOptions{
			AllowClusterScoped: true,
		})

	secret.OnChange(ctx, "registration-expire", h.OnSecretChange)
	clusters.OnChange(ctx, "cluster-to-clusterregistration", h.OnCluster)
	clusters.Cache().AddIndexer(clusterByClientID, func(obj *fleet.Cluster) ([]string, error) {
		return []string{
			fmt.Sprintf("%s/%s", obj.Namespace, obj.Spec.ClientID),
		}, nil
	})
	clusterRegistration.Cache().AddIndexer(clusterRegistrationByClientID, func(obj *fleet.ClusterRegistration) ([]string, error) {
		return []string{
			fmt.Sprintf("%s/%s", obj.Namespace, obj.Spec.ClientID),
		}, nil
	})
	relatedresource.Watch(ctx, "sa-to-cluster-registration", saToClusterRegistration, clusterRegistration, serviceAccount)
}

func saToClusterRegistration(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
	if sa, ok := obj.(*v1.ServiceAccount); ok {
		ns := sa.Annotations[fleet.ClusterRegistrationNamespaceAnnotation]
		name := sa.Annotations[fleet.ClusterRegistrationAnnotation]
		if ns != "" && name != "" {
			return []relatedresource.Key{{
				Namespace: ns,
				Name:      name,
			}}, nil
		}
	}
	return nil, nil
}

func (h *handler) OnCluster(key string, cluster *fleet.Cluster) (*fleet.Cluster, error) {
	if cluster == nil || cluster.Status.Namespace == "" {
		return cluster, nil
	}

	crs, err := h.clusterRegistration.Cache().GetByIndex(clusterRegistrationByClientID,
		fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Spec.ClientID))
	if err != nil {
		return nil, err
	}
	for _, cr := range crs {
		if !cr.Status.Granted {
			logrus.Infof("Namespace assigned to %s/%s triggering %s/%s", cluster.Namespace, cluster.Name,
				cr.Namespace, cr.Name)
			h.clusterRegistration.Enqueue(cr.Namespace, cr.Name)
		}
	}

	return cluster, nil
}

func (h *handler) authorizeCluster(sa *v1.ServiceAccount, cluster *fleet.Cluster, req *fleet.ClusterRegistration) (*v1.Secret, error) {
	var secret *v1.Secret
	var err error
	if len(sa.Secrets) != 0 {
		secret, err = h.secretsCache.Get(sa.Namespace, sa.Secrets[0].Name)
		if apierrors.IsNotFound(err) {
			// secrets can be slow to propagate to the cache
			secret, err = h.secrets.Get(sa.Namespace, sa.Secrets[0].Name, metav1.GetOptions{})
		}
	} else {
		secret, err = secretutil.GetServiceAccountTokenSecret(sa, h.secrets)
	}
	if err != nil || secret == nil {
		return nil, err
	}
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registration.SecretName(req.Spec.ClientID, req.Spec.ClientRandom),
			Namespace: h.systemRegistrationNamespace,
			Labels: map[string]string{
				fleet.ClusterAnnotation: cluster.Name,
			},
			Annotations: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Type: AgentCredentialSecretType,
		Data: map[string][]byte{
			"token":               secret.Data["token"],
			"deploymentNamespace": []byte(cluster.Status.Namespace),
			"clusterNamespace":    []byte(cluster.Namespace),
			"clusterName":         []byte(cluster.Name),
			"systemNamespace":     []byte(h.systemNamespace),
		},
	}, nil
}

func (h *handler) OnSecretChange(key string, secret *v1.Secret) (*v1.Secret, error) {
	if secret == nil || secret.Namespace != h.systemRegistrationNamespace ||
		secret.Labels[fleet.ClusterAnnotation] == "" {
		return secret, nil
	}

	if time.Since(secret.CreationTimestamp.Time) > deleteSecretAfter {
		logrus.Infof("Deleting expired registration secret %s/%s", secret.Namespace, secret.Name)
		return secret, h.secrets.Delete(secret.Namespace, secret.Name, nil)
	}

	h.secrets.EnqueueAfter(secret.Namespace, secret.Name, deleteSecretAfter/2)
	return secret, nil
}

func (h *handler) OnChange(request *fleet.ClusterRegistration, status fleet.ClusterRegistrationStatus) ([]runtime.Object, fleet.ClusterRegistrationStatus, error) {
	var (
		objects []runtime.Object
	)

	if status.Granted {
		// only create the cluster for the request once
		return nil, status, generic.ErrSkip
	}

	cluster, err := h.createOrGetCluster(request)
	if err != nil || cluster == nil {
		return nil, status, err
	}

	if cluster.Status.Namespace == "" {
		status.ClusterName = cluster.Name
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
		}
	}

	logrus.Infof("Cluster registration %s/%s, cluster %s/%s granted [%v]",
		request.Namespace, request.Name, cluster.Namespace, cluster.Name, status.Granted)

	status.ClusterName = cluster.Name
	return append(objects,
		&v1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: cluster.Status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedLabel:                           "true",
					fleet.ClusterAnnotation:                      cluster.Name,
					fleet.ClusterRegistrationAnnotation:          request.Name,
					fleet.ClusterRegistrationNamespaceAnnotation: request.Namespace,
				},
			},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: request.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
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
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: cluster.Status.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
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
				Name:     "fleet-bundle-deployment",
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: request.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
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
	if !config.Get().IgnoreClusterRegistrationLabels {
		for k, v := range request.Spec.ClusterLabels {
			labels[k] = v
		}
	}
	labels[fleet.ClusterAnnotation] = clusterName

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
	if apierrors.IsAlreadyExists(err) {
		return h.clusters.Get(request.Namespace, clusterName, metav1.GetOptions{})
	}
	if err == nil {
		logrus.Infof("Created cluster %s/%s", request.Namespace, clusterName)
	}
	return cluster, err
}

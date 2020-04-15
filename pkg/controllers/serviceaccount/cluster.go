package serviceaccount

import (
	"fmt"

	"github.com/rancher/fleet/pkg/controllers/sharedindex"

	"github.com/rancher/fleet/pkg/controllers/role"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/registration"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *handler) OnChangeForCluster(key string, sa *v1.ServiceAccount) (*v1.ServiceAccount, error) {
	if sa == nil {
		return sa, h.apply.WithOwnerKey(key, schema.GroupVersionKind{
			Group:   v1.GroupName,
			Version: v1.SchemeGroupVersion.Version,
			Kind:    "ServiceAccount",
		}).ApplyObjects()
	}

	requestName := sa.Annotations[fleet.RequestAnnotation]
	if requestName == "" {
		return sa, nil
	}

	clusterName := sa.Annotations[fleet.ClusterAnnotation]
	if clusterName == "" {
		return sa, nil
	}

	if deleted, err := h.deleteExpired(sa); err != nil || deleted {
		return sa, err
	}

	cg, err := h.clusterGroup.GetByIndex(sharedindex.ClusterGroupByNamespace, sa.Namespace)
	if err != nil {
		return nil, err
	}
	if len(cg) == 0 {
		return nil, fmt.Errorf("failed to find cluster group for namespace %s", sa.Namespace)
	}

	cluster, err := h.clusters.Get(cg[0].Status.Namespace, clusterName)
	if apierrors.IsNotFound(err) {
		logrus.Errorf("invalid service account %s/%s for missing cluster %s/%s", sa.Namespace, sa.Name, cg[0].Status.Namespace, clusterName)
		return sa, nil
	} else if err != nil {
		return sa, err
	}

	request, err := h.requests.Get(sa.Namespace, requestName)
	if err != nil {
		// ignore error
		return sa, nil
	}

	return sa, h.authorizeCluster(sa, cluster, request)
}

func (h *handler) authorizeCluster(sa *v1.ServiceAccount, cg *fleet.Cluster, req *fleet.ClusterRegistrationRequest) error {
	if len(sa.Secrets) == 0 {
		return nil
	}
	secret, err := h.secretsCache.Get(sa.Namespace, sa.Secrets[0].Name)
	if apierrors.IsNotFound(err) {
		// secrets can be slow to propagate to the cache
		secret, err = h.secrets.Get(sa.Namespace, sa.Secrets[0].Name, metav1.GetOptions{})
	}
	if err != nil {
		return err
	}
	return h.apply.WithOwner(sa).WithSetOwnerReference(true, false).ApplyObjects(
		&v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      registration.SecretName(req.Spec.ClientID, req.Spec.ClientRandom),
				Namespace: sa.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Type: role.AgentCredentialSecretType,
			Data: map[string][]byte{
				"token":     secret.Data["token"],
				"namespace": []byte(cg.Status.Namespace),
			},
		})
}

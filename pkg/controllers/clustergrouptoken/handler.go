package clustergrouptoken

import (
	"context"
	"strconv"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	clusterGroupToken   fleetcontrollers.ClusterGroupTokenClient
	clusterGroupCache   fleetcontrollers.ClusterGroupCache
	serviceAccountCache corecontrollers.ServiceAccountCache
}

func Register(ctx context.Context,
	apply apply.Apply,
	clusterGroupToken fleetcontrollers.ClusterGroupTokenController,
	clusterGroupCache fleetcontrollers.ClusterGroupCache,
	serviceAccounts corecontrollers.ServiceAccountController,
) {
	h := &handler{
		clusterGroupToken:   clusterGroupToken,
		clusterGroupCache:   clusterGroupCache,
		serviceAccountCache: serviceAccounts.Cache(),
	}

	fleetcontrollers.RegisterClusterGroupTokenGeneratingHandler(ctx,
		clusterGroupToken,
		apply.WithCacheTypes(serviceAccounts),
		"",
		"cluster-group-token",
		h.OnChange,
		nil)

	relatedresource.Watch(ctx, "sa-to-cgt", serviceAccountToClusterGroupToken, clusterGroupToken, serviceAccounts)
}

func serviceAccountToClusterGroupToken(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
	if sa, ok := obj.(*corev1.ServiceAccount); ok {
		cgt := sa.Annotations[fleet.ClusterGroupTokenAnnotation]
		if cgt == "" {
			return nil, nil
		}
		return []relatedresource.Key{
			{
				Namespace: namespace,
				Name:      cgt,
			},
		}, nil
	}
	return nil, nil
}

func (h *handler) OnChange(token *fleet.ClusterGroupToken, status fleet.ClusterGroupTokenStatus) ([]runtime.Object, fleet.ClusterGroupTokenStatus, error) {
	if gone, err := h.deleteExpired(token); err != nil || gone {
		return nil, status, nil
	}

	status.SecretName = ""
	if token.Spec.ClusterGroupName == "" {
		return nil, status, nil
	}

	cg, err := h.clusterGroupCache.Get(token.Namespace, token.Spec.ClusterGroupName)
	if apierror.IsNotFound(err) {
		return nil, status, nil
	} else if err != nil {
		return nil, status, err
	}

	saName := name.SafeConcatName(token.Name, "token")
	secretName := ""

	sa, err := h.serviceAccountCache.Get(token.Namespace, saName)
	if apierror.IsNotFound(err) {
		// secret doesn't exist
	} else if err != nil {
		return nil, status, err
	} else if len(sa.Secrets) > 0 {
		secretName = sa.Secrets[0].Name
	}

	status.SecretName = secretName
	return []runtime.Object{
		&corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: token.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation:           "true",
					fleet.ClusterGroupAnnotation:      cg.Name,
					fleet.ClusterGroupTokenAnnotation: token.Name,
				},
			},
		},
	}, status, nil
}

func (h *handler) deleteExpired(token *fleet.ClusterGroupToken) (bool, error) {
	ttlSecondStr := token.Annotations[fleet.TTLSecondsAnnotation]
	if ttlSecondStr == "" || ttlSecondStr == "0" {
		return false, nil
	}

	ttl, err := strconv.Atoi(ttlSecondStr)
	if err != nil {
		logrus.Errorf("Invalid TTL on %s/%s: %v", token.Namespace, token.Name, err)
		return true, h.clusterGroupToken.Delete(token.Namespace, token.Name, nil)
	}

	expire := token.CreationTimestamp.Add(time.Second * time.Duration(ttl))
	if time.Now().After(expire) {
		return true, h.clusterGroupToken.Delete(token.Namespace, token.Name, nil)
	}

	return false, nil
}

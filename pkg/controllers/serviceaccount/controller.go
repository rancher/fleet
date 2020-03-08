package serviceaccount

import (
	"context"
	"strconv"
	"time"

	rbaccontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/rbac/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

type handler struct {
	apply          apply.Apply
	serviceAccount corecontrollers.ServiceAccountController
	clusterGroup   fleetcontrollers.ClusterGroupCache
	clusters       fleetcontrollers.ClusterCache
	requests       fleetcontrollers.ClusterRegistrationRequestCache
	secrets        corecontrollers.SecretCache
}

func Register(ctx context.Context,
	apply apply.Apply,
	role rbaccontrollers.RoleController,
	roleBinding rbaccontrollers.RoleBindingController,
	serviceAccount corecontrollers.ServiceAccountController,
	requests fleetcontrollers.ClusterRegistrationRequestController,
	clusters fleetcontrollers.ClusterCache,
	secrets corecontrollers.SecretController,
	clusterGroup fleetcontrollers.ClusterGroupCache) {
	h := &handler{
		apply: apply.
			WithCacheTypes(role, roleBinding, secrets),
		serviceAccount: serviceAccount,
		clusterGroup:   clusterGroup,
		clusters:       clusters,
		requests:       requests.Cache(),
		secrets:        secrets.Cache(),
	}

	serviceAccount.OnChange(ctx, "serviceaccount-sa-cluster", h.OnChangeForCluster)
	serviceAccount.OnChange(ctx, "serviceaccount-sa-clustergroup", h.OnChangeForClusterGroup)
}

func (h *handler) deleteExpired(sa *v1.ServiceAccount) (bool, error) {
	ttlSecondStr := sa.Annotations[fleet.TTLSecondsAnnotation]
	if ttlSecondStr == "" || ttlSecondStr == "0" {
		return false, nil
	}

	ttl, err := strconv.Atoi(ttlSecondStr)
	if err != nil {
		logrus.Errorf("Invalid TTL on %s/%s: %v", sa.Namespace, sa.Name, err)
		return true, h.serviceAccount.Delete(sa.Namespace, sa.Name, nil)
	}

	expire := sa.CreationTimestamp.Add(time.Second * time.Duration(ttl))
	if time.Now().After(expire) {
		return true, h.serviceAccount.Delete(sa.Namespace, sa.Name, nil)
	}

	return false, nil
}

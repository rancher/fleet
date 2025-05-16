// Package clusterstatus updates the cluster.fleet.cattle.io status in the upstream cluster with the current cluster status.
package clusterstatus

import (
	"context"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"

	"github.com/rancher/wrangler/v3/pkg/ticker"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type handler struct {
	agentNamespace   string
	clusterName      string
	clusterNamespace string
	client           client.Client
	reported         fleet.AgentStatus
}

func Ticker(ctx context.Context, client client.Client, agentNamespace string, clusterNamespace string, clusterName string, checkinInterval time.Duration) {
	logger := log.FromContext(ctx).WithName("clusterstatus").WithValues("cluster", clusterName, "interval", checkinInterval)

	h := handler{
		agentNamespace:   agentNamespace,
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,
		client:           client,
	}

	go func() {
		time.Sleep(durations.ClusterRegisterDelay)
		logger.V(1).Info("Reporting cluster status once")
		if err := h.Update(ctx); err != nil {
			logger.Error(err, "failed to report initial cluster status")
		}
	}()
	go func() {
		if checkinInterval == 0 {
			checkinInterval = durations.DefaultClusterCheckInterval
		}
		for range ticker.Context(ctx, checkinInterval) {
			logger.V(1).Info("Reporting cluster status")
			if err := h.Update(ctx); err != nil {
				logger.Error(err, "failed to report cluster status")
			}
		}
	}()
}

// Update the cluster.fleet.cattle.io status in the upstream cluster with the current cluster status
func (h *handler) Update(ctx context.Context) error {
	agentStatus := fleet.AgentStatus{
		LastSeen:  metav1.Now(),
		Namespace: h.agentNamespace,
	}

	if equality.Semantic.DeepEqual(h.reported, agentStatus) {
		return nil
	}

	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.clusterName,
			Namespace: h.clusterNamespace,
		},
	}

	// Create a patch with the updated status, we avoid Get as that would
	// need additional RBAC
	patch := `[{"op":"add","path":"/status/agent","value":{"lastSeen":"` +
		agentStatus.LastSeen.Format(time.RFC3339) +
		`","namespace":"` + agentStatus.Namespace +
		`"}}]`

	err := h.client.Status().Patch(ctx, cluster, client.RawPatch(types.JSONPatchType, []byte(patch)))
	if err != nil {
		return err
	}

	h.reported = agentStatus
	return nil
}

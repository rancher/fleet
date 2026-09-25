// Package clusterstatus updates the cluster.fleet.cattle.io status in the upstream cluster with the current cluster status.
package clusterstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/version"

	"github.com/rancher/wrangler/v3/pkg/ticker"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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

	// Guard before both goroutines — checkinInterval must be positive to be
	// used as a jitter bound.
	if checkinInterval <= 0 {
		checkinInterval = durations.DefaultClusterCheckInterval
	}

	go func() {
		// Fixed registration delay plus a small bounded jitter, so the first
		// status report stays fast regardless of checkinInterval. Only the
		// periodic ticker below is jittered across the full interval.
		delay := durations.ClusterRegisterDelay + wait.Jitter(durations.ClusterRegisterJitterMax, 1.0)
		if !sleepOrDone(ctx, delay) {
			return
		}
		logger.V(1).Info("Reporting cluster status once")
		if err := h.Update(ctx); err != nil {
			logger.Error(err, "failed to report initial cluster status")
		}
	}()
	go func() {
		// Spread agent check-ins across the interval window to prevent a thundering
		// herd on the fleet-controller when many agents start at the same time.
		if !sleepOrDone(ctx, wait.Jitter(checkinInterval, 1.0)) {
			return
		}
		for range ticker.Context(ctx, checkinInterval) {
			logger.V(1).Info("Reporting cluster status")
			if err := h.Update(ctx); err != nil {
				logger.Error(err, "failed to report cluster status")
			}
		}
	}()
}

// sleepOrDone waits for d or until ctx is cancelled, whichever comes first.
// It returns false if ctx was cancelled first.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// Update the cluster.fleet.cattle.io status in the upstream cluster with the current cluster status
func (h *handler) Update(ctx context.Context) error {
	agentStatus := fleet.AgentStatus{
		LastSeen:  metav1.Now(),
		Namespace: h.agentNamespace,
		Version:   version.Version,
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

	value, err := json.Marshal(agentStatus)
	if err != nil {
		return err
	}

	// Create a patch with the updated status, we avoid Get as that would
	// need additional RBAC
	patch := fmt.Sprintf(`[{"op":"add","path":"/status/agent","value":%s}]`, value)

	if err := h.client.Status().Patch(ctx, cluster, client.RawPatch(types.JSONPatchType, []byte(patch))); err != nil {
		return err
	}

	h.reported = agentStatus
	return nil
}

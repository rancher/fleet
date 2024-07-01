// Package clusterstatus updates the cluster.fleet.cattle.io status in the upstream cluster with the current cluster status.
package clusterstatus

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v3/pkg/ticker"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type handler struct {
	agentNamespace   string
	clusterName      string
	clusterNamespace string
	nodes            corecontrollers.NodeClient
	clusters         fleetcontrollers.ClusterClient
	reported         fleet.AgentStatus
}

func Ticker(ctx context.Context,
	agentNamespace string,
	clusterNamespace string,
	clusterName string,
	checkinInterval time.Duration,
	nodes corecontrollers.NodeClient,
	clusters fleetcontrollers.ClusterClient) {

	logger := log.FromContext(ctx).WithName("clusterstatus").WithValues("cluster", clusterName, "interval", checkinInterval)

	h := handler{
		agentNamespace:   agentNamespace,
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,
		nodes:            nodes,
		clusters:         clusters,
	}

	go func() {
		time.Sleep(durations.ClusterRegisterDelay)
		logger.V(1).Info("Reporting cluster status once")
		if err := h.Update(); err != nil {
			logrus.Errorf("failed to report cluster status: %v", err)
		}
	}()
	go func() {
		if checkinInterval == 0 {
			checkinInterval = durations.DefaultClusterCheckInterval
		}
		for range ticker.Context(ctx, checkinInterval) {
			logger.V(1).Info("Reporting cluster status")
			if err := h.Update(); err != nil {
				logrus.Errorf("failed to report cluster status: %v", err)
			}
		}
	}()
}

// Update the cluster.fleet.cattle.io status in the upstream cluster with the current cluster status
func (h *handler) Update() error {
	agentStatus := fleet.AgentStatus{
		LastSeen:  metav1.Now(),
		Namespace: h.agentNamespace,
	}

	if equality.Semantic.DeepEqual(h.reported, agentStatus) {
		return nil
	}

	data, err := json.Marshal(fleet.Cluster{
		Status: fleet.ClusterStatus{
			Agent: agentStatus,
		},
	})
	if err != nil {
		return err
	}

	_, err = h.clusters.Patch(h.clusterNamespace, h.clusterName, types.MergePatchType, data, "status")
	if err != nil {
		return err
	}

	h.reported = agentStatus
	return nil
}

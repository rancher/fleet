// Package cluster updates the cluster.fleet.cattle.io status in the upstream cluster with the current node status. (fleetagent)
package cluster

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/ticker"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

type handler struct {
	agentNamespace   string
	clusterName      string
	clusterNamespace string
	nodes            corecontrollers.NodeCache
	clusters         fleetcontrollers.ClusterClient
	reported         fleet.AgentStatus
}

func Register(ctx context.Context,
	agentNamespace string,
	clusterNamespace string,
	clusterName string,
	checkinInterval time.Duration,
	nodes corecontrollers.NodeCache,
	clusters fleetcontrollers.ClusterClient) {

	h := handler{
		agentNamespace:   agentNamespace,
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,
		nodes:            nodes,
		clusters:         clusters,
	}

	go func() {
		time.Sleep(durations.ClusterRegisterDelay)
		if err := h.Update(); err != nil {
			logrus.Errorf("failed to report cluster node status: %v", err)
		}
	}()
	go func() {
		if checkinInterval == 0 {
			checkinInterval = durations.DefaultClusterCheckInterval
		}
		for range ticker.Context(ctx, checkinInterval) {
			if err := h.Update(); err != nil {
				logrus.Errorf("failed to report cluster node status: %v", err)
			}
		}
	}()
}

// Update the cluster.fleet.cattle.io status in the upstream cluster with the current node status
func (h *handler) Update() error {
	nodes, err := h.nodes.List(labels.Everything())
	if err != nil {
		return err
	}

	ready, nonReady := sortReadyUnready(nodes)

	agentStatus := fleet.AgentStatus{
		LastSeen:      metav1.Now(),
		Namespace:     h.agentNamespace,
		NonReadyNodes: len(nonReady),
		ReadyNodes:    len(ready),
	}

	if len(ready) > 3 {
		ready = ready[:3]
	}
	if len(nonReady) > 3 {
		nonReady = nonReady[:3]
	}

	agentStatus.ReadyNodeNames = ready
	agentStatus.NonReadyNodeNames = nonReady

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

func sortReadyUnready(nodes []*corev1.Node) (ready []string, nonReady []string) {
	var (
		masterNodeNames         []string
		nonReadyMasterNodeNames []string
		readyNodes              []string
		nonReadyNodes           []string
	)

	for _, node := range nodes {
		ready := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}

		if node.Annotations["node-role.kubernetes.io/master"] == "true" {
			if ready {
				masterNodeNames = append(masterNodeNames, node.Name)
			} else {
				nonReadyMasterNodeNames = append(nonReadyMasterNodeNames, node.Name)
			}
		} else {
			if ready {
				readyNodes = append(readyNodes, node.Name)
			} else {
				nonReadyNodes = append(nonReadyNodes, node.Name)
			}
		}
	}

	sort.Strings(masterNodeNames)
	sort.Strings(nonReadyMasterNodeNames)
	sort.Strings(readyNodes)
	sort.Strings(nonReadyNodes)

	return append(masterNodeNames, readyNodes...), append(nonReadyMasterNodeNames, nonReadyNodes...)
}

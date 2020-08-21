package display

import (
	"context"
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
)

type handler struct {
}

func Register(ctx context.Context, clusters fleetcontrollers.ClusterController) {
	h := &handler{}

	fleetcontrollers.RegisterClusterStatusHandler(ctx, clusters, "", "cluster-display", h.OnClusterChange)
}

func (h *handler) OnClusterChange(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, error) {
	status.Display.ReadyBundles = fmt.Sprintf("%d/%d",
		cluster.Status.Summary.Ready,
		cluster.Status.Summary.DesiredReady)
	status.Display.ReadyNodes = fmt.Sprintf("%d/%d",
		cluster.Status.Agent.ReadyNodes,
		cluster.Status.Agent.NonReadyNodes+cluster.Status.Agent.ReadyNodes)
	status.Display.SampleNode = sampleNode(status)
	return status, nil
}

func sampleNode(status fleet.ClusterStatus) string {
	if len(status.Agent.ReadyNodeNames) > 0 {
		return status.Agent.ReadyNodeNames[0]
	}
	if len(status.Agent.NonReadyNodeNames) > 0 {
		return status.Agent.NonReadyNodeNames[0]
	}
	return ""
}

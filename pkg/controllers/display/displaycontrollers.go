package display

import (
	"context"
	"fmt"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/genericcondition"
)

type handler struct {
}

func Register(ctx context.Context,
	clusters fleetcontrollers.ClusterController,
	clustergroups fleetcontrollers.ClusterGroupController,
	gitrepos fleetcontrollers.GitRepoController,
	bundledeployments fleetcontrollers.BundleDeploymentController,
	bundles fleetcontrollers.BundleController) {
	h := &handler{}

	fleetcontrollers.RegisterClusterStatusHandler(ctx, clusters, "", "cluster-display", h.OnClusterChange)
	fleetcontrollers.RegisterClusterGroupStatusHandler(ctx, clustergroups, "", "clustergroup-display", h.OnClusterGroupChange)
	fleetcontrollers.RegisterGitRepoStatusHandler(ctx, gitrepos, "", "gitrepo-display", h.OnRepoChange)
	fleetcontrollers.RegisterBundleDeploymentStatusHandler(ctx, bundledeployments, "", "bundledeployment-display", h.OnBundleDeploymentChange)
	fleetcontrollers.RegisterBundleStatusHandler(ctx, bundles, "", "bundle-display", h.OnBundleChange)
}

func (h *handler) OnBundleChange(_ *fleet.Bundle, status fleet.BundleStatus) (fleet.BundleStatus, error) {
	status.Display.ReadyClusters = fmt.Sprintf("%d/%d",
		status.Summary.Ready,
		status.Summary.DesiredReady)
	return status, nil
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

func (h *handler) OnClusterGroupChange(cluster *fleet.ClusterGroup, status fleet.ClusterGroupStatus) (fleet.ClusterGroupStatus, error) {
	status.Display.ReadyBundles = fmt.Sprintf("%d/%d",
		cluster.Status.Summary.Ready,
		cluster.Status.Summary.DesiredReady)
	status.Display.ReadyClusters = fmt.Sprintf("%d/%d",
		cluster.Status.ClusterCount-cluster.Status.NonReadyClusterCount,
		cluster.Status.ClusterCount)
	if len(cluster.Status.NonReadyClusters) > 0 {
		status.Display.ReadyClusters += " (" + strings.Join(cluster.Status.NonReadyClusters, ",") + ")"
	}
	return status, nil
}

func (h *handler) OnRepoChange(gitrepo *fleet.GitRepo, status fleet.GitRepoStatus) (fleet.GitRepoStatus, error) {
	status.Display.ReadyBundles = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)
	return status, nil
}

func (h *handler) OnBundleDeploymentChange(_ *fleet.BundleDeployment, status fleet.BundleDeploymentStatus) (fleet.BundleDeploymentStatus, error) {
	var (
		deployed, monitored string
	)

	for _, cond := range status.Conditions {
		switch cond.Type {
		case "Deployed":
			deployed = conditionToMessage(cond)
		case "Monitored":
			monitored = conditionToMessage(cond)
		}
	}

	status.Display = fleet.BundleDeploymentDisplay{
		Deployed:  deployed,
		Monitored: monitored,
	}

	return status, nil
}

func conditionToMessage(cond genericcondition.GenericCondition) string {
	if cond.Reason == "Error" {
		return "Error: " + cond.Message
	}
	return string(cond.Status)
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

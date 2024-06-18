package reconciler

import (
	"fmt"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/sirupsen/logrus"
)

const (
	maxNew = 50
)

func resetStatus(status *fleet.BundleStatus, allTargets []*target.Target) (err error) {
	status.MaxNew = maxNew
	status.Summary = fleet.BundleSummary{}
	status.PartitionStatus = nil
	status.Unavailable = 0
	status.NewlyCreated = 0
	status.Summary = target.Summary(allTargets)
	status.Unavailable = target.Unavailable(allTargets)
	status.MaxUnavailable, err = target.MaxUnavailable(allTargets)
	return err
}

func updateDisplay(status *fleet.BundleStatus) {
	status.Display.ReadyClusters = fmt.Sprintf("%d/%d",
		status.Summary.Ready,
		status.Summary.DesiredReady)
	status.Display.State = string(summary.GetSummaryState(status.Summary))
}

// setResourceKey updates status.ResourceKey from the bundleDeployments
func setResourceKey(status *fleet.BundleStatus, allTargets []*target.Target) {
	keys := []fleet.ResourceKey{}
	for _, target := range allTargets {
		if target.Deployment == nil {
			continue
		}
		if target.Deployment.Namespace == "" {
			logrus.Infof("Skipping bundledeployment with empty namespace %q", target.Deployment.Name)
			continue
		}
		for _, resource := range target.Deployment.Status.Resources {
			key := fleet.ResourceKey{
				Name:       resource.Name,
				Namespace:  resource.Namespace,
				APIVersion: resource.APIVersion,
				Kind:       resource.Kind,
			}
			keys = append(keys, key)
		}
	}
	status.ResourceKey = keys
}

package reconciler

import (
	"fmt"

	"github.com/rancher/fleet/internal/cmd/controller/bundleevents"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func resetStatus(status *fleet.BundleStatus, allTargets []*target.Target) (err error) {
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

// failureProfile counts and describes the failing deployments of all targets,
// the way the summary counts them, but without its limit on how many it
// describes.
func failureProfile(targets []*target.Target) *bundleevents.Profile {
	profile := &bundleevents.Profile{}
	for _, t := range targets {
		cluster := t.Cluster.Namespace + "/" + t.Cluster.Name
		profile.Add(cluster, t.State(), t.Message(), t.NonReady())
	}

	return profile
}

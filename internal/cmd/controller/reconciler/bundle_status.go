package reconciler

import (
	"fmt"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// defaultMaxNew mirrors the default in the target package, kept in sync for
// the status field which reflects the configured value.
const defaultMaxNew = 50

func resetStatus(status *fleet.BundleStatus, allTargets []*target.Target, rollout *fleet.RolloutStrategy) (err error) {
	status.MaxNew = defaultMaxNew
	if rollout != nil && rollout.MaxNew != nil {
		status.MaxNew = *rollout.MaxNew
	}
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

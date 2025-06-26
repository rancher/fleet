package target

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/util/intstr"
)

// Summary calculates a fleet.BundleSummary from targets (pure function)
func Summary(targets []*Target) fleet.BundleSummary {
	var bundleSummary fleet.BundleSummary
	for _, currentTarget := range targets {
		cluster := currentTarget.Cluster.Namespace + "/" + currentTarget.Cluster.Name
		summary.IncrementState(&bundleSummary, cluster, currentTarget.state(), currentTarget.message(), currentTarget.modified(), currentTarget.nonReady())
		bundleSummary.DesiredReady++
	}
	return bundleSummary
}

// MaxUnavailable returns the maximum number of unavailable deployments given the targets rollout strategy (pure function)
func MaxUnavailable(targets []*Target) (int, error) {
	rollout := getRollout(targets)
	return limit(len(targets), rollout.MaxUnavailable)
}

// Unavailable counts the number of targets that are not available (pure function)
func Unavailable(targets []*Target) (count int) {
	for _, target := range targets {
		if target.Deployment == nil {
			continue
		}
		if isUnavailable(target.Deployment) {
			count++
		}
	}
	return
}

// updatePartitionStatus recomputes and sets the status.Unavailable counter
// and returns true if the partition is unavailable, e.g. there are more
// unavailable targets than the maximum set (does not mutate targets)
func updatePartitionStatus(partitionStatus *fleet.PartitionStatus, targets []*Target) bool {
	// Unavailable for a partition is stricter than unavailable for a target.
	// For a partition a target must be available and up-to-date.
	partitionStatus.Unavailable = 0
	for _, target := range targets {
		if !upToDate(target) || isUnavailable(target.Deployment) {
			partitionStatus.Unavailable++
		}
	}

	return partitionStatus.Unavailable > partitionStatus.MaxUnavailable
}

// upToDate returns true if the target is up to date (pure function)
func upToDate(target *Target) bool {
	if target.Deployment == nil ||
		target.Deployment.Spec.StagedDeploymentID != target.DeploymentID ||
		target.Deployment.Spec.DeploymentID != target.DeploymentID ||
		target.Deployment.Status.AppliedDeploymentID != target.DeploymentID {
		return false
	}

	return true
}

// isUnavailable checks if target is available (pure function). If no target is
// provided, it returns true, assuming that a nil target is always available.
func isUnavailable(target *fleet.BundleDeployment) bool {
	if target == nil {
		return false
	}
	return target.Status.AppliedDeploymentID != target.Spec.DeploymentID ||
		!target.Status.Ready
}

// limit calculates the maximum number of unavailable items. It uses the first
// non-nil value from the provided values. If no value is provided, it defaults
// to a predefined limit. If a percentage is provided, it calculates the
// percentage of the total count of items. If the percentage is less than or
// equal to zero, it defaults to 1.
//
// The resulting percentage is rounded down to the nearest integer.
func limit(count int, val ...*intstr.IntOrString) (int, error) {
	if count <= 0 {
		return 1, nil
	}

	var maxUnavailable *intstr.IntOrString

	for _, val := range val {
		if val != nil {
			maxUnavailable = val
			break
		}
	}

	if maxUnavailable == nil {
		maxUnavailable = &defLimit
	}

	if maxUnavailable.Type == intstr.Int {
		return maxUnavailable.IntValue(), nil
	}

	i := maxUnavailable.IntValue()
	if i > 0 {
		return i, nil
	}

	if !strings.HasSuffix(maxUnavailable.StrVal, "%") {
		return 0, fmt.Errorf("invalid maxUnavailable, must be int or percentage (ending with %%): %s", maxUnavailable)
	}

	percent, err := strconv.ParseFloat(strings.TrimSuffix(maxUnavailable.StrVal, "%"), 64)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to parse %s", maxUnavailable.StrVal)
	}

	if percent <= 0 {
		return 1, nil
	}

	i = int(float64(count)*percent) / 100
	if i <= 0 {
		return 1, nil
	}

	return i, nil
}

package summary

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/rancher/wrangler/pkg/genericcondition"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/condition"
)

func IncrementState(summary *fleet.BundleSummary, name string, state fleet.BundleState, message string) {
	switch state {
	case fleet.Modified:
		summary.Modified++
	case fleet.Pending:
		summary.Pending++
	case fleet.NotApplied:
		summary.NotApplied++
	case fleet.NotReady:
		summary.NotReady++
	case fleet.OutOfSync:
		summary.OutOfSync++
	case fleet.Ready:
		summary.Ready++
	}
	if name != "" {
		if summary.NonReadyNames == nil {
			summary.NonReadyNames = map[string]fleet.BundleStateDescription{}
		}
		if len(summary.NonReadyNames) < 10 {
			summary.NonReadyNames[name] = fleet.BundleStateDescription{
				State:   state,
				Message: message,
			}
		}
	}
}

func IsReady(summary fleet.BundleSummary) bool {
	return summary.DesiredReady == summary.Ready
}

func Increment(left *fleet.BundleSummary, right fleet.BundleSummary) {
	left.NotReady += right.NotReady
	left.NotApplied += right.NotApplied
	left.OutOfSync += right.OutOfSync
	left.Modified += right.Modified
	left.Ready += right.Ready
	left.Pending += right.Pending
	left.DesiredReady += right.DesiredReady
	for k, v := range right.NonReadyNames {
		if left.NonReadyNames == nil {
			left.NonReadyNames = map[string]fleet.BundleStateDescription{}
		}
		left.NonReadyNames[k] = v
	}
}

func GetDeploymentState(bundleDeployment *fleet.BundleDeployment) fleet.BundleState {
	switch {
	case bundleDeployment.Status.AppliedDeploymentID != bundleDeployment.Spec.DeploymentID:
		return fleet.NotApplied
	case !bundleDeployment.Status.Ready:
		return fleet.NotReady
	case bundleDeployment.Spec.DeploymentID != bundleDeployment.Spec.StagedDeploymentID:
		return fleet.OutOfSync
	case !bundleDeployment.Status.NonModified:
		return fleet.Modified
	default:
		return fleet.Ready
	}
}

func SetReadyConditions(obj interface{}, summary fleet.BundleSummary) {
	if reflect.ValueOf(obj).Kind() != reflect.Ptr {
		panic("obj passed must be a pointer")
	}
	c := condition.Cond("Ready")
	msg := ReadyMessage(summary)
	c.SetStatusBool(obj, len(msg) == 0)
	c.Message(obj, msg)
}

func ReadyMessageFromCondition(conds []genericcondition.GenericCondition) string {
	for _, cond := range conds {
		if cond.Type == "Ready" {
			return cond.Message
		}
	}
	return ""
}

func ReadyMessage(summary fleet.BundleSummary) string {
	message := &strings.Builder{}
	for msg, count := range map[fleet.BundleState]int{
		fleet.OutOfSync:  summary.OutOfSync,
		fleet.NotReady:   summary.NotReady,
		fleet.NotApplied: summary.NotApplied,
		fleet.Pending:    summary.Pending,
		fleet.Modified:   summary.Modified,
	} {
		if count <= 0 {
			continue
		}
		if message.Len() > 0 {
			message.WriteString("; ")
		}
		for k, v := range summary.NonReadyNames {
			if v.State == msg {
				if count > 1 {
					k += "..."
				}
				message.WriteString(fmt.Sprintf("%s: %d (%s: %s)", msg, count, k, v.Message))
				break
			}
		}
	}

	return message.String()
}

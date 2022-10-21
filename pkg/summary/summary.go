// Package summary provides a summary of a bundle's state. (fleetcontroller)
package summary

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/pkg/condition"
	"github.com/rancher/wrangler/pkg/genericcondition"
)

func IncrementState(summary *fleet.BundleSummary, name string, state fleet.BundleState, message string, modified []fleet.ModifiedStatus, nonReady []fleet.NonReadyStatus) {
	switch state {
	case fleet.Modified:
		summary.Modified++
	case fleet.Pending:
		summary.Pending++
	case fleet.WaitApplied:
		summary.WaitApplied++
	case fleet.ErrApplied:
		summary.ErrApplied++
	case fleet.NotReady:
		summary.NotReady++
	case fleet.OutOfSync:
		summary.OutOfSync++
	case fleet.Ready:
		summary.Ready++
	}
	if name != "" && state != fleet.Ready {
		if len(summary.NonReadyResources) < 10 {
			summary.NonReadyResources = append(summary.NonReadyResources, fleet.NonReadyResource{
				Name:           name,
				State:          state,
				Message:        message,
				ModifiedStatus: modified,
				NonReadyStatus: nonReady,
			})
		}
	}
}

func IsReady(summary fleet.BundleSummary) bool {
	return summary.DesiredReady == summary.Ready
}

func Increment(left *fleet.BundleSummary, right fleet.BundleSummary) {
	left.NotReady += right.NotReady
	left.WaitApplied += right.WaitApplied
	left.ErrApplied += right.ErrApplied
	left.OutOfSync += right.OutOfSync
	left.Modified += right.Modified
	left.Ready += right.Ready
	left.Pending += right.Pending
	left.DesiredReady += right.DesiredReady
	if len(left.NonReadyResources) < 10 {
		left.NonReadyResources = append(left.NonReadyResources, right.NonReadyResources...)
	}
}

func IncrementResourceCounts(left *fleet.GitRepoResourceCounts, right fleet.GitRepoResourceCounts) {
	left.Ready += right.Ready
	left.DesiredReady += right.DesiredReady
	left.WaitApplied += right.WaitApplied
	left.Modified += right.Modified
	left.Orphaned += right.Orphaned
	left.Missing += right.Missing
	left.Unknown += right.Unknown
	left.NotReady += right.NotReady
}

func GetSummaryState(summary fleet.BundleSummary) fleet.BundleState {
	var state fleet.BundleState
	for _, nonReady := range summary.NonReadyResources {
		if fleet.StateRank[nonReady.State] > fleet.StateRank[state] {
			state = nonReady.State
		}
	}
	return state
}

func GetDeploymentState(bundleDeployment *fleet.BundleDeployment) fleet.BundleState {
	switch {
	case bundleDeployment.Status.AppliedDeploymentID != bundleDeployment.Spec.DeploymentID:
		if condition.Cond(fleet.BundleDeploymentConditionDeployed).IsFalse(bundleDeployment) {
			return fleet.ErrApplied
		}
		return fleet.WaitApplied
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

func SetReadyConditions(obj interface{}, referencedKind string, summary fleet.BundleSummary) {
	if reflect.ValueOf(obj).Kind() != reflect.Ptr {
		panic("obj passed must be a pointer")
	}
	c := condition.Cond("Ready")
	msg := ReadyMessage(summary, referencedKind)
	c.SetStatusBool(obj, len(msg) == 0)
	c.Message(obj, msg)
}

func MessageFromCondition(conditionType string, conds []genericcondition.GenericCondition) string {
	for _, cond := range conds {
		if cond.Type == conditionType {
			return cond.Message
		}
	}
	return ""
}

func MessageFromDeployment(deployment *fleet.BundleDeployment) string {
	if deployment == nil {
		return ""
	}
	message := MessageFromCondition("Deployed", deployment.Status.Conditions)
	if message == "" {
		message = MessageFromCondition("Installed", deployment.Status.Conditions)
	}
	if message == "" {
		message = MessageFromCondition("Monitored", deployment.Status.Conditions)
	}
	return message
}

func ReadyMessage(summary fleet.BundleSummary, referencedKind string) string {
	var messages []string
	for msg, count := range map[fleet.BundleState]int{
		fleet.OutOfSync:   summary.OutOfSync,
		fleet.NotReady:    summary.NotReady,
		fleet.WaitApplied: summary.WaitApplied,
		fleet.ErrApplied:  summary.ErrApplied,
		fleet.Pending:     summary.Pending,
		fleet.Modified:    summary.Modified,
	} {
		if count <= 0 {
			continue
		}
		for _, v := range summary.NonReadyResources {
			name := v.Name
			if v.State == msg {
				if len(v.Message) == 0 {
					messages = append(messages, fmt.Sprintf("%s(%d) [%s %s]", msg, count, referencedKind, name))
				} else {
					messages = append(messages, fmt.Sprintf("%s(%d) [%s %s: %s]", msg, count, referencedKind, name, v.Message))
				}
				for i, m := range v.ModifiedStatus {
					if i > 3 {
						break
					}
					messages = append(messages, m.String())
				}
				for i, m := range v.NonReadyStatus {
					if i > 3 {
						break
					}
					messages = append(messages, m.String())
				}
				break
			}
		}
	}

	sort.Strings(messages)
	return strings.Join(messages, "; ")
}

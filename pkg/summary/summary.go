package summary

import (
	"fmt"
	"reflect"
	"sort"
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
		if len(summary.NonReadyResources) < 10 {
			summary.NonReadyResources = append(summary.NonReadyResources, fleet.NonReadyResource{
				Name:    name,
				State:   state,
				Message: message,
			})
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
	if len(left.NonReadyResources) < 10 {
		left.NonReadyResources = append(left.NonReadyResources, right.NonReadyResources...)
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
		message = MessageFromCondition("Monitored", deployment.Status.Conditions)
	}
	return message
}

func ReadyMessage(summary fleet.BundleSummary) string {
	var messages []string
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
		for _, v := range summary.NonReadyResources {
			name := v.Name
			if v.State == msg {
				if count > 1 {
					name += "..."
				}
				messages = append(messages, fmt.Sprintf("%s: %d (%s %s)", msg, count, name, v.Message))
				break
			}
		}
	}

	sort.Strings(messages)
	return strings.Join(messages, "; ")
}

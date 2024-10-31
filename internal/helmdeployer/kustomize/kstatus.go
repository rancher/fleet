package kustomize

import (
	"strings"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/data"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/summary"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
)

func init() {
	summary.Summarizers = append(summary.Summarizers, KStatusSummarizer)
}

func KStatusSummarizer(obj data.Object, conditions []summary.Condition, summary fleetv1.Summary) fleetv1.Summary {
	result, err := status.Compute(&unstructured.Unstructured{Object: obj})
	if err != nil {
		return summary
	}

	switch result.Status {
	case status.InProgressStatus:
		summary.Transitioning = true
	case status.FailedStatus:
		summary.Error = true
	case status.CurrentStatus:
	case status.TerminatingStatus:
		summary.Transitioning = true
	case status.UnknownStatus:
	}

	if result.Message != "" {
		// Deduplicate status messages (https://github.com/rancher/fleet/issues/2859)
		messages := make(map[string]bool)
		var resultMessages []string
		for _, message := range strings.Split(result.Message, ";") {
			if _, ok := messages[message]; ok {
				continue
			}
			messages[message] = true
			resultMessages = append(resultMessages, message)
		}

		summary.Message = append(summary.Message, strings.Join(resultMessages, ";"))
	}

	return summary
}

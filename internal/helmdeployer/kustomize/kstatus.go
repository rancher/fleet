package kustomize

import (
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
		summary.Message = append(summary.Message, result.Message)
	}

	return summary
}

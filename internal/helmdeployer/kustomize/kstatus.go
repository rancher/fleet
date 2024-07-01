package kustomize

import (
	"github.com/rancher/wrangler/v3/pkg/data"
	"github.com/rancher/wrangler/v3/pkg/summary"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
)

func init() {
	summary.Summarizers = append(summary.Summarizers, KStatusSummarizer)
}

func KStatusSummarizer(obj data.Object, conditions []summary.Condition, summary summary.Summary) summary.Summary {
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

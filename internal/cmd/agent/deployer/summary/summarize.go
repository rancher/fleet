package summary

import (
	"strings"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/data"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"

	unstructured2 "github.com/rancher/wrangler/v3/pkg/unstructured"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func dedupMessage(messages []string) []string {
	seen := map[string]bool{}
	var result []string

	for _, message := range messages {
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		if seen[message] {
			continue
		}
		seen[message] = true
		result = append(result, message)
	}

	return result
}

func Summarize(runtimeObj runtime.Object) fleetv1.Summary {
	var (
		obj     data.Object
		err     error
		summary fleetv1.Summary
	)

	if s, ok := runtimeObj.(*SummarizedObject); ok {
		return s.Summary
	}

	unstr, ok := runtimeObj.(*unstructured.Unstructured)
	if !ok {
		unstr, err = unstructured2.ToUnstructured(runtimeObj)
		if err != nil {
			return summary
		}
	}

	if unstr != nil {
		obj = unstr.Object
	}

	conditions := getConditions(obj)

	for _, summarizer := range Summarizers {
		summary = summarizer(obj, conditions, summary)
	}

	if summary.State == "" {
		summary.State = "active"
	}

	summary.State = strings.ToLower(summary.State)
	summary.Message = dedupMessage(summary.Message)
	return summary
}

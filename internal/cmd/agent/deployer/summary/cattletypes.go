package summary

import (
	"github.com/rancher/fleet/internal/cmd/agent/deployer/data"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"
)

func checkCattleTypes(obj data.Object, condition []Condition, summary fleetv1.Summary) fleetv1.Summary {
	return checkRelease(obj, condition, summary)
}

func checkRelease(obj data.Object, _ []Condition, summary fleetv1.Summary) fleetv1.Summary {
	if !isKind(obj, "App", "catalog.cattle.io") {
		return summary
	}
	if obj.String("status", "summary", "state") != "deployed" {
		return summary
	}
	for _, resources := range obj.Slice("spec", "resources") {
		summary.Relationships = append(summary.Relationships, fleetv1.Relationship{
			Name:       resources.String("name"),
			Kind:       resources.String("kind"),
			APIVersion: resources.String("apiVersion"),
			Type:       "helmresource",
		})
	}
	return summary
}

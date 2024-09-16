package summary

import (
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SummarizedObject struct {
	metav1.PartialObjectMetadata
	fleetv1.Summary
}

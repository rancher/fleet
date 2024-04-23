package summary_test

import (
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestGetSummaryState(t *testing.T) {
	// It is supposed to return an empty string if there are no non-ready
	// resources, independent of the state of the bundle.
	s := fleet.BundleSummary{
		Modified:     1,
		Pending:      2,
		WaitApplied:  3,
		ErrApplied:   4,
		NotReady:     5,
		OutOfSync:    6,
		Ready:        7,
		DesiredReady: 8,
	}
	bundleState := summary.GetSummaryState(s)
	if string(bundleState) != "" {
		t.Errorf("Expected empty string, got %s", bundleState)
	}

	// It is supposed to return "Modified" if there is a non-ready resource in
	// state Modified.
	s.NonReadyResources = []fleet.NonReadyResource{
		{
			Name:  "test",
			State: fleet.Modified,
		},
	}
	bundleState = summary.GetSummaryState(s)
	if bundleState != fleet.Modified {
		t.Errorf("Expected Modified, got %s", bundleState)
	}

	// It is supposed to return the highest priority state if there are multiple
	// non-ready resources. Rank depends on v1alpha1.StateRank.
	// ErrApplied:  7,
	// WaitApplied: 6,
	// Modified:    5,
	// OutOfSync:   4,
	// Pending:     3,
	// NotReady:    2,
	// Ready:       1,
	s.NonReadyResources = []fleet.NonReadyResource{
		{
			Name:  "test",
			State: fleet.Pending,
		},
		{
			Name:  "test",
			State: fleet.WaitApplied,
		},
	}
	bundleState = summary.GetSummaryState(s)
	if bundleState != fleet.WaitApplied {
		t.Errorf("Expected WaitApplied, got %s", bundleState)
	}
}

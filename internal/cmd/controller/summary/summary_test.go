package summary_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"
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

// TestSetReadyConditions_ReasonNotClearedWhenBecomingReady tests that the Reason is
// cleared when transitioning from error to ready state in SetReadyConditions.
func TestSetReadyConditions_ReasonClearedWhenBecomingReady(t *testing.T) {
	// Create a BundleStatus (which has Conditions field)
	bundleStatus := &fleet.BundleStatus{}

	// Simulate an error state by using SetError
	c := condition.Cond("Ready")
	c.SetError(bundleStatus, "", fmt.Errorf("some error occurred"))

	// Verify the error state is set correctly
	if c.GetStatus(bundleStatus) != "False" {
		t.Errorf("Expected status 'False' after SetError, got %q", c.GetStatus(bundleStatus))
	}
	if c.GetReason(bundleStatus) != "Error" {
		t.Errorf("Expected reason 'Error' after SetError, got %q", c.GetReason(bundleStatus))
	}
	if c.GetMessage(bundleStatus) != "some error occurred" {
		t.Errorf("Expected message 'some error occurred' after SetError, got %q", c.GetMessage(bundleStatus))
	}

	// Now the resource becomes ready - create an empty summary (all resources ready)
	readySummary := fleet.BundleSummary{
		Ready:        5,
		DesiredReady: 5,
		// No NonReadyResources means everything is ready
	}

	// Call SetReadyConditions which should transition to ready state
	summary.SetReadyConditions(bundleStatus, "Cluster", readySummary)

	// Verify the status is now True (ready)
	if c.GetStatus(bundleStatus) != "True" {
		t.Errorf("Expected status 'True' after SetReadyConditions, got %q", c.GetStatus(bundleStatus))
	}

	// Verify the message is empty (ready)
	if c.GetMessage(bundleStatus) != "" {
		t.Errorf("Expected empty message after SetReadyConditions, got %q", c.GetMessage(bundleStatus))
	}

	// Verify the Reason is cleared
	if c.GetReason(bundleStatus) != "" {
		t.Errorf("Expected empty reason when Ready status is True, but got %q.",
			c.GetReason(bundleStatus))
	}
}

func setCondition(bd *fleet.BundleDeployment, condType, message string) {
	condition.Cond(condType).SetError(bd, "", errors.New(message))
}

func TestMessageFromDeployment(t *testing.T) {
	t.Run("nil deployment returns empty string", func(t *testing.T) {
		if msg := summary.MessageFromDeployment(nil); msg != "" {
			t.Errorf("expected empty string, got %q", msg)
		}
	})

	t.Run("Deployed condition takes priority over Installed", func(t *testing.T) {
		bd := &fleet.BundleDeployment{}
		bd.Spec.DeploymentID = "id1"
		bd.Status.AppliedDeploymentID = "id1"
		setCondition(bd, "Deployed", "deploy error")
		setCondition(bd, "Installed", "install error")
		if msg := summary.MessageFromDeployment(bd); msg != "deploy error" {
			t.Errorf("expected deploy error to take priority, got %q", msg)
		}
	})

	t.Run("Installed shown when deployment IDs match", func(t *testing.T) {
		bd := &fleet.BundleDeployment{}
		bd.Spec.DeploymentID = "id1"
		bd.Status.AppliedDeploymentID = "id1"
		setCondition(bd, "Installed", "install error")
		if msg := summary.MessageFromDeployment(bd); msg != "install error" {
			t.Errorf("expected install error, got %q", msg)
		}
	})

	t.Run("Installed suppressed when deployment IDs differ", func(t *testing.T) {
		bd := &fleet.BundleDeployment{}
		bd.Spec.DeploymentID = "id2"
		bd.Status.AppliedDeploymentID = "id1"
		setCondition(bd, "Installed", "stale install error")
		if msg := summary.MessageFromDeployment(bd); msg != "" {
			t.Errorf("expected stale Installed message to be suppressed, got %q", msg)
		}
	})

	t.Run("Monitored used as fallback when Deployed and Installed are absent", func(t *testing.T) {
		bd := &fleet.BundleDeployment{}
		bd.Spec.DeploymentID = "id1"
		bd.Status.AppliedDeploymentID = "id1"
		setCondition(bd, "Monitored", "monitor error")
		if msg := summary.MessageFromDeployment(bd); msg != "monitor error" {
			t.Errorf("expected monitor error as fallback, got %q", msg)
		}
	})
}

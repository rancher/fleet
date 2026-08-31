package summary_test

import (
	"errors"
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
	// ErrApplied:           8,
	// WaitingForDependency: 7,
	// WaitApplied:          6,
	// Modified:             5,
	// OutOfSync:            4,
	// Pending:              3,
	// NotReady:             2,
	// Ready:                1,
	//
	// The winning state is deliberately placed at a different index in each
	// case below, so that an implementation picking the first or the last
	// element instead of the highest ranked one fails here.
	s.NonReadyResources = []fleet.NonReadyResource{
		{
			Name:  "test",
			State: fleet.WaitApplied,
		},
		{
			Name:  "test",
			State: fleet.Pending,
		},
	}
	bundleState = summary.GetSummaryState(s)
	if bundleState != fleet.WaitApplied {
		t.Errorf("Expected WaitApplied, got %s", bundleState)
	}

	// WaitingForDependency outranks the generic WaitApplied, because it says
	// why the deployment is waiting.
	s.NonReadyResources = []fleet.NonReadyResource{
		{
			Name:  "test",
			State: fleet.WaitApplied,
		},
		{
			Name:  "test",
			State: fleet.WaitingForDependency,
		},
		{
			Name:  "test",
			State: fleet.NotReady,
		},
	}
	bundleState = summary.GetSummaryState(s)
	if bundleState != fleet.WaitingForDependency {
		t.Errorf("Expected WaitingForDependency, got %s", bundleState)
	}

	// An actual deployment error still outranks a dependency wait, so it is
	// not hidden in summaries covering several clusters.
	s.NonReadyResources = []fleet.NonReadyResource{
		{
			Name:  "test",
			State: fleet.ErrApplied,
		},
		{
			Name:  "test",
			State: fleet.WaitingForDependency,
		},
	}
	bundleState = summary.GetSummaryState(s)
	if bundleState != fleet.ErrApplied {
		t.Errorf("Expected ErrApplied, got %s", bundleState)
	}

	// The single Modified case above is unavoidably both first and last, so
	// pin the ranking once more with Modified surrounded by lower ranked
	// states.
	s.NonReadyResources = []fleet.NonReadyResource{
		{
			Name:  "test",
			State: fleet.OutOfSync,
		},
		{
			Name:  "test",
			State: fleet.Modified,
		},
		{
			Name:  "test",
			State: fleet.Pending,
		},
	}
	bundleState = summary.GetSummaryState(s)
	if bundleState != fleet.Modified {
		t.Errorf("Expected Modified, got %s", bundleState)
	}
}

func TestGetDeploymentState(t *testing.T) {
	// deployed builds a bundledeployment which has not applied its
	// deployment ID yet, with the given Deployed condition.
	notApplied := func(status, reason string) *fleet.BundleDeployment {
		bd := &fleet.BundleDeployment{}
		bd.Spec.DeploymentID = "id2"
		bd.Status.AppliedDeploymentID = "id1"
		if status != "" {
			c := condition.Cond(fleet.BundleDeploymentConditionDeployed)
			c.SetStatus(bd, status)
			c.Reason(bd, reason)
		}
		return bd
	}

	tests := map[string]struct {
		bd   *fleet.BundleDeployment
		want fleet.BundleState
	}{
		"waiting on a dependency is not an error": {
			bd:   notApplied("False", fleet.BundleDeploymentReasonWaitingForDependency),
			want: fleet.WaitingForDependency,
		},
		"a failed deployment is still ErrApplied": {
			bd:   notApplied("False", "Error"),
			want: fleet.ErrApplied,
		},
		"a failed deployment without a reason is still ErrApplied": {
			bd:   notApplied("False", ""),
			want: fleet.ErrApplied,
		},
		"no Deployed condition yet": {
			bd:   notApplied("", ""),
			want: fleet.WaitApplied,
		},
		"Deployed is true, but the deployment ID is not applied yet": {
			bd:   notApplied("True", ""),
			want: fleet.WaitApplied,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := summary.GetDeploymentState(tc.bd); got != tc.want {
				t.Errorf("GetDeploymentState() = %s, want %s", got, tc.want)
			}
		})
	}

	// A stale WaitingForDependency reason must not survive a successful
	// deployment: once the deployment ID is applied, the state is derived
	// from the resources.
	t.Run("reason is ignored once the deployment ID is applied", func(t *testing.T) {
		bd := notApplied("False", fleet.BundleDeploymentReasonWaitingForDependency)
		bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
		bd.Spec.StagedDeploymentID = bd.Spec.DeploymentID
		bd.Status.Ready = true
		bd.Status.NonModified = true

		if got := summary.GetDeploymentState(bd); got != fleet.Ready {
			t.Errorf("GetDeploymentState() = %s, want Ready", got)
		}
	})
}

func TestReadyMessage_WaitingForDependency(t *testing.T) {
	s := fleet.BundleSummary{
		WaitingForDependency: 1,
		NonReadyResources: []fleet.NonReadyResource{
			{
				Name:    "fleet-local/local",
				State:   fleet.WaitingForDependency,
				Message: "waiting for dependent bundle(s) to reach an accepted state: dep (state: NotReady, accepted: Ready)",
			},
		},
	}

	msg := summary.ReadyMessage(s, "Cluster")
	want := "WaitingForDependency(1) [Cluster fleet-local/local: waiting for dependent bundle(s) to reach an accepted state: dep (state: NotReady, accepted: Ready)]"
	if msg != want {
		t.Errorf("ReadyMessage() = %q, want %q", msg, want)
	}
}

func TestIncrementState_WaitingForDependency(t *testing.T) {
	s := &fleet.BundleSummary{}
	summary.IncrementState(s, "bd", fleet.WaitingForDependency, "waiting", nil, nil)

	if s.WaitingForDependency != 1 {
		t.Errorf("expected WaitingForDependency count 1, got %d", s.WaitingForDependency)
	}
	if len(s.NonReadyResources) != 1 || s.NonReadyResources[0].State != fleet.WaitingForDependency {
		t.Errorf("expected the bundledeployment to be listed as non-ready, got %+v", s.NonReadyResources)
	}

	other := fleet.BundleSummary{WaitingForDependency: 2}
	summary.Increment(s, other)
	if s.WaitingForDependency != 3 {
		t.Errorf("expected WaitingForDependency count 3 after Increment, got %d", s.WaitingForDependency)
	}
}

// TestSetReadyConditions_ReasonNotClearedWhenBecomingReady tests that the Reason is
// cleared when transitioning from error to ready state in SetReadyConditions.
func TestSetReadyConditions_ReasonClearedWhenBecomingReady(t *testing.T) {
	// Create a BundleStatus (which has Conditions field)
	bundleStatus := &fleet.BundleStatus{}

	// Simulate an error state by using SetError
	c := condition.Cond("Ready")
	c.SetError(bundleStatus, "", errors.New("some error occurred"))

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

package bundlereader

import (
	"strings"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateFleetYAML_ValidAcceptedStates(t *testing.T) {
	tests := []struct {
		name   string
		states []fleet.BundleState
	}{
		{
			name:   "empty states (defaults to Ready)",
			states: nil,
		},
		{
			name:   "Ready only",
			states: []fleet.BundleState{fleet.Ready},
		},
		{
			name:   "Ready and Modified",
			states: []fleet.BundleState{fleet.Ready, fleet.Modified},
		},
		{
			name:   "all valid states",
			states: []fleet.BundleState{fleet.Ready, fleet.NotReady, fleet.WaitApplied, fleet.ErrApplied, fleet.OutOfSync, fleet.Pending, fleet.Modified},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					DependsOn: []fleet.BundleRef{
						{
							Name:           "my-dependency",
							AcceptedStates: tt.states,
						},
					},
				},
			}

			err := validateFleetYAML(fy)
			if err != nil {
				t.Errorf("validateFleetYAML() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateFleetYAML_InvalidAcceptedStates(t *testing.T) {
	tests := []struct {
		name          string
		states        []fleet.BundleState
		expectedError string
	}{
		{
			name:          "invalid state",
			states:        []fleet.BundleState{"InvalidState"},
			expectedError: `dependsOn[0].acceptedStates[0]: invalid state "InvalidState"`,
		},
		{
			name:          "typo in Ready",
			states:        []fleet.BundleState{"ready"}, // lowercase
			expectedError: `dependsOn[0].acceptedStates[0]: invalid state "ready"`,
		},
		{
			name:          "mixed valid and invalid",
			states:        []fleet.BundleState{fleet.Ready, "Foo", fleet.Modified},
			expectedError: `dependsOn[0].acceptedStates[1]: invalid state "Foo"`,
		},
		{
			name:          "empty string state",
			states:        []fleet.BundleState{""},
			expectedError: `dependsOn[0].acceptedStates[0]: invalid state ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					DependsOn: []fleet.BundleRef{
						{
							Name:           "my-dependency",
							AcceptedStates: tt.states,
						},
					},
				},
			}

			err := validateFleetYAML(fy)
			if err == nil {
				t.Errorf("validateFleetYAML() expected error containing %q, got nil", tt.expectedError)
				return
			}
			if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("validateFleetYAML() error = %q, expected to contain %q", err.Error(), tt.expectedError)
			}
		})
	}
}

func TestValidateFleetYAML_MissingNameAndSelector(t *testing.T) {
	fy := &fleet.FleetYAML{
		BundleSpec: fleet.BundleSpec{
			DependsOn: []fleet.BundleRef{
				{
					// Neither name nor selector specified
					AcceptedStates: []fleet.BundleState{fleet.Ready},
				},
			},
		},
	}

	err := validateFleetYAML(fy)
	if err == nil {
		t.Error("validateFleetYAML() expected error for missing name/selector, got nil")
		return
	}
	expectedError := "must specify either 'name' or 'selector'"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("validateFleetYAML() error = %q, expected to contain %q", err.Error(), expectedError)
	}
}

func TestValidateFleetYAML_ValidWithSelector(t *testing.T) {
	fy := &fleet.FleetYAML{
		BundleSpec: fleet.BundleSpec{
			DependsOn: []fleet.BundleRef{
				{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "database"},
					},
					AcceptedStates: []fleet.BundleState{fleet.Ready, fleet.Modified},
				},
			},
		},
	}

	err := validateFleetYAML(fy)
	if err != nil {
		t.Errorf("validateFleetYAML() unexpected error: %v", err)
	}
}

func TestIsValidBundleState(t *testing.T) {
	validStates := []fleet.BundleState{
		fleet.Ready,
		fleet.NotReady,
		fleet.WaitApplied,
		fleet.ErrApplied,
		fleet.OutOfSync,
		fleet.Pending,
		fleet.Modified,
	}

	for _, state := range validStates {
		if !isValidBundleState(state) {
			t.Errorf("isValidBundleState(%q) = false, expected true", state)
		}
	}

	invalidStates := []fleet.BundleState{
		"Invalid",
		"ready",
		"READY",
		"",
		"Foo",
	}

	for _, state := range invalidStates {
		if isValidBundleState(state) {
			t.Errorf("isValidBundleState(%q) = true, expected false", state)
		}
	}
}

func TestValidBundleStatesList_SortedByRank(t *testing.T) {
	states := validBundleStatesList()

	// Verify all states from StateRank are present
	if len(states) != len(fleet.StateRank) {
		t.Errorf("validBundleStatesList() returned %d states, expected %d", len(states), len(fleet.StateRank))
	}

	// Verify states are sorted by rank (ascending)
	for i := 1; i < len(states); i++ {
		prevRank := fleet.StateRank[states[i-1]]
		currRank := fleet.StateRank[states[i]]
		if prevRank > currRank {
			t.Errorf("validBundleStatesList() not sorted: %q (rank %d) comes before %q (rank %d)",
				states[i-1], prevRank, states[i], currRank)
		}
	}
}

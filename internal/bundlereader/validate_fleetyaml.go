package bundlereader

import (
	"fmt"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// validateFleetYAML validates the semantic content of a parsed FleetYAML.
// It returns an error if any field contains invalid values.
func validateFleetYAML(fy *fleet.FleetYAML) error {
	// Validate DependsOn entries at the bundle level
	for i, dep := range fy.DependsOn {
		if err := validateBundleRef(i, dep); err != nil {
			return err
		}
	}

	return nil
}

// validateBundleRef validates a single BundleRef entry
func validateBundleRef(index int, dep fleet.BundleRef) error {
	// Validate that at least name or selector is specified
	if dep.Name == "" && dep.Selector == nil {
		return fmt.Errorf("dependsOn[%d]: must specify either 'name' or 'selector'", index)
	}

	// Validate AcceptedStates against the known valid states
	for j, state := range dep.AcceptedStates {
		if !isValidBundleState(state) {
			return fmt.Errorf(
				"dependsOn[%d].acceptedStates[%d]: invalid state %q, valid values are: %v",
				index, j, state, validBundleStatesList(),
			)
		}
	}
	return nil
}

// isValidBundleState checks if a BundleState is valid by checking against StateRank
func isValidBundleState(state fleet.BundleState) bool {
	_, exists := fleet.StateRank[state]
	return exists
}

// validBundleStatesList returns a list of valid bundle states for error messages.
// It derives the list directly from StateRank to avoid maintaining duplicate lists.
// States are sorted by rank (Ready first, ErrApplied last).
func validBundleStatesList() []fleet.BundleState {
	states := make([]fleet.BundleState, 0, len(fleet.StateRank))
	for state := range fleet.StateRank {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		return fleet.StateRank[states[i]] < fleet.StateRank[states[j]]
	})
	return states
}

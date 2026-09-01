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

// statesRejectedAsAccepted lists states which are ranked in StateRank, but which
// must not be used as an acceptedState. WaitingForDependency describes a bundle
// which is itself blocked on one of its own dependencies, so accepting it would
// unblock the dependent bundle precisely when its dependency stopped making
// progress, which defeats the point of declaring the dependency.
var statesRejectedAsAccepted = map[fleet.BundleState]struct{}{
	fleet.WaitingForDependency: {},
}

// isValidBundleState checks if a BundleState may be used as an acceptedState. A
// state is valid if it is ranked in StateRank and is not rejected explicitly.
func isValidBundleState(state fleet.BundleState) bool {
	if _, rejected := statesRejectedAsAccepted[state]; rejected {
		return false
	}
	_, exists := fleet.StateRank[state]
	return exists
}

// validBundleStatesList returns a list of valid bundle states for error messages.
// It derives the list directly from StateRank to avoid maintaining duplicate lists.
// States are sorted by rank (Ready first, ErrApplied last).
func validBundleStatesList() []fleet.BundleState {
	states := make([]fleet.BundleState, 0, len(fleet.StateRank))
	for state := range fleet.StateRank {
		if !isValidBundleState(state) {
			continue
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		return fleet.StateRank[states[i]] < fleet.StateRank[states[j]]
	})
	return states
}

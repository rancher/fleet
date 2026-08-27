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

func diffWithPatchName(name string) *fleet.DiffOptions {
	return &fleet.DiffOptions{
		ComparePatches: []fleet.ComparePatch{
			{
				APIVersion: "v1",
				Kind:       "Service",
				Name:       name,
				Operations: []fleet.Operation{{Op: "ignore"}},
			},
		},
	}
}

func TestValidateFleetYAML_ValidComparePatchNames(t *testing.T) {
	tests := []struct {
		name      string
		patchName string
	}{
		{name: "unset name", patchName: ""},
		{name: "literal name", patchName: "my-app"},
		{name: "literal name with dots and dashes", patchName: "app.v1-alpha.svc"},
		{name: "regular expression", patchName: ".*serv.*"},
		{name: "anchored regular expression", patchName: "^my-app-[0-9]+$"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: diffWithPatchName(tt.patchName),
					},
				},
			}

			if err := validateFleetYAML(fy); err != nil {
				t.Errorf("validateFleetYAML() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateFleetYAML_InvalidComparePatchNames(t *testing.T) {
	tests := []struct {
		name          string
		patchName     string
		expectedError string
	}{
		{
			name:          "unclosed character class",
			patchName:     "foo[",
			expectedError: `diff.comparePatches[0].name: invalid regular expression "foo[": error parsing regexp: missing closing ]`,
		},
		{
			name:          "unclosed group",
			patchName:     "a(b",
			expectedError: `diff.comparePatches[0].name: invalid regular expression "a(b": error parsing regexp: missing closing )`,
		},
		{
			name:          "leading repetition operator",
			patchName:     "*x",
			expectedError: `diff.comparePatches[0].name: invalid regular expression "*x": error parsing regexp: missing argument to repetition operator`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: diffWithPatchName(tt.patchName),
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

// TestValidateFleetYAML_ComparePatchNameWithoutIgnoreOp pins the deliberate choice
// that the rule holds for every comparePatch, not only for patches carrying an
// "ignore" operation: the name of a patch which is only ever matched exactly must
// still compile as a regular expression.
func TestValidateFleetYAML_ComparePatchNameWithoutIgnoreOp(t *testing.T) {
	tests := []struct {
		name  string
		patch fleet.ComparePatch
	}{
		{
			name: "jsonPointers, no operations",
			patch: fleet.ComparePatch{
				APIVersion:   "v1",
				Kind:         "Service",
				Name:         "foo[",
				JsonPointers: []string{"/spec/clusterIP"},
			},
		},
		{
			name: "remove operation",
			patch: fleet.ComparePatch{
				APIVersion: "v1",
				Kind:       "Service",
				Name:       "foo[",
				Operations: []fleet.Operation{{Op: "remove", Path: "/spec/clusterIP"}},
			},
		},
	}

	expectedError := `diff.comparePatches[0].name: invalid regular expression "foo["`

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: &fleet.DiffOptions{ComparePatches: []fleet.ComparePatch{tt.patch}},
					},
				},
			}

			err := validateFleetYAML(fy)
			if err == nil {
				t.Errorf("validateFleetYAML() expected error containing %q, got nil", expectedError)
				return
			}
			if !strings.Contains(err.Error(), expectedError) {
				t.Errorf("validateFleetYAML() error = %q, expected to contain %q", err.Error(), expectedError)
			}
		})
	}
}

// TestValidateFleetYAML_ComparePatchNameOptionSites makes sure every
// BundleDeploymentOptions reachable from fleet.yaml is walked, and that the error
// names the site it came from.
func TestValidateFleetYAML_ComparePatchNameOptionSites(t *testing.T) {
	tests := []struct {
		name          string
		fy            *fleet.FleetYAML
		expectedError string
	}{
		{
			name: "bundle level",
			fy: &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: diffWithPatchName("foo["),
					},
				},
			},
			expectedError: `diff.comparePatches[0].name: invalid regular expression "foo["`,
		},
		{
			name: "targets",
			fy: &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					Targets: []fleet.BundleTarget{
						{Name: "first"},
						{
							Name: "second",
							BundleDeploymentOptions: fleet.BundleDeploymentOptions{
								Diff: diffWithPatchName("a(b"),
							},
						},
					},
				},
			},
			expectedError: `targets[1].diff.comparePatches[0].name: invalid regular expression "a(b"`,
		},
		{
			name: "targetCustomizations",
			fy: &fleet.FleetYAML{
				TargetCustomizations: []fleet.BundleTarget{
					{Name: "first"},
					{
						Name: "second",
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Diff: diffWithPatchName("*x"),
						},
					},
				},
			},
			expectedError: `targetCustomizations[1].diff.comparePatches[0].name: invalid regular expression "*x"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFleetYAML(tt.fy)
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

// TestValidateFleetYAML_ComparePatchNameIndex checks that the reported index is the
// index of the offending patch, not of the first one.
func TestValidateFleetYAML_ComparePatchNameIndex(t *testing.T) {
	fy := &fleet.FleetYAML{
		BundleSpec: fleet.BundleSpec{
			BundleDeploymentOptions: fleet.BundleDeploymentOptions{
				Diff: &fleet.DiffOptions{
					ComparePatches: []fleet.ComparePatch{
						{Name: "my-app"},
						{Name: ".*serv.*"},
						{Name: "foo["},
					},
				},
			},
		},
	}

	err := validateFleetYAML(fy)
	if err == nil {
		t.Fatal("validateFleetYAML() expected error, got nil")
	}
	expectedError := `diff.comparePatches[2].name: invalid regular expression "foo["`
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("validateFleetYAML() error = %q, expected to contain %q", err.Error(), expectedError)
	}
}

// diffWithPatchOps builds a diff with a single comparePatch carrying the given
// operations, so the tests below only state the op values they are about.
func diffWithPatchOps(ops ...string) *fleet.DiffOptions {
	patch := fleet.ComparePatch{Name: "my-app"}
	for _, op := range ops {
		patch.Operations = append(patch.Operations, fleet.Operation{Op: op, Path: "/spec/replicas"})
	}

	return &fleet.DiffOptions{ComparePatches: []fleet.ComparePatch{patch}}
}

func TestValidateFleetYAML_ValidComparePatchOperations(t *testing.T) {
	tests := []struct {
		name string
		diff *fleet.DiffOptions
	}{
		{name: "no diff at all", diff: nil},
		{name: "patch without operations", diff: diffWithPatchOps()},
		{name: "ignore", diff: diffWithPatchOps("ignore")},
		{name: "remove", diff: diffWithPatchOps("remove")},
		{name: "add", diff: diffWithPatchOps("add")},
		{name: "replace", diff: diffWithPatchOps("replace")},
		{name: "test", diff: diffWithPatchOps("test")},
		{name: "several operations in one patch", diff: diffWithPatchOps("remove", "replace", "test")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: tt.diff,
					},
				},
			}

			if err := validateFleetYAML(fy); err != nil {
				t.Errorf("validateFleetYAML() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateFleetYAML_InvalidComparePatchOperations(t *testing.T) {
	tests := []struct {
		name          string
		diff          *fleet.DiffOptions
		expectedError string
	}{
		{
			name:          "unknown op",
			diff:          diffWithPatchOps("bogus"),
			expectedError: `diff.comparePatches[0].operations[0].op: unsupported operation "bogus", valid values are: add, ignore, remove, replace, test`,
		},
		{
			name:          "trailing space",
			diff:          diffWithPatchOps("remove "),
			expectedError: `diff.comparePatches[0].operations[0].op: unsupported operation "remove "`,
		},
		{
			name:          "wrong case",
			diff:          diffWithPatchOps("Remove"),
			expectedError: `diff.comparePatches[0].operations[0].op: unsupported operation "Remove"`,
		},
		{
			// An operation with a path but no op reads as a no-op and is not one:
			// it makes the whole patch fail. Rejecting it is a deliberate
			// tightening over what fleet apply accepted before.
			name:          "empty op",
			diff:          diffWithPatchOps(""),
			expectedError: `diff.comparePatches[0].operations[0].op: unsupported operation ""`,
		},
		{
			name:          "second operation is the offending one",
			diff:          diffWithPatchOps("remove", "bogus"),
			expectedError: `diff.comparePatches[0].operations[1].op: unsupported operation "bogus"`,
		},
		{
			// json-patch implements "copy" and "move", but fleet.Operation has no
			// "from" field, so neither can ever be encoded in a form json-patch
			// accepts: both always fail with "missing from field".
			name:          "copy, which Fleet cannot encode",
			diff:          diffWithPatchOps("copy"),
			expectedError: `diff.comparePatches[0].operations[0].op: unsupported operation "copy"`,
		},
		{
			name:          "move, which Fleet cannot encode",
			diff:          diffWithPatchOps("move"),
			expectedError: `diff.comparePatches[0].operations[0].op: unsupported operation "move"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: tt.diff,
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

func TestValidateFleetYAML_ComparePatchOperationOptionSites(t *testing.T) {
	tests := []struct {
		name          string
		fy            *fleet.FleetYAML
		expectedError string
	}{
		{
			name: "bundle level",
			fy: &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: diffWithPatchOps("bogus"),
					},
				},
			},
			expectedError: `diff.comparePatches[0].operations[0].op: unsupported operation "bogus"`,
		},
		{
			name: "targets",
			fy: &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					Targets: []fleet.BundleTarget{
						{Name: "first"},
						{
							Name: "second",
							BundleDeploymentOptions: fleet.BundleDeploymentOptions{
								Diff: diffWithPatchOps("remove", "delete"),
							},
						},
					},
				},
			},
			expectedError: `targets[1].diff.comparePatches[0].operations[1].op: unsupported operation "delete"`,
		},
		{
			name: "targetCustomizations",
			fy: &fleet.FleetYAML{
				TargetCustomizations: []fleet.BundleTarget{
					{Name: "first"},
					{
						Name: "second",
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Diff: diffWithPatchOps(""),
						},
					},
				},
			},
			expectedError: `targetCustomizations[1].diff.comparePatches[0].operations[0].op: unsupported operation ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFleetYAML(tt.fy)
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

// TestValidateFleetYAML_ComparePatchOperationIndex checks that the reported index
// is the index of the offending patch, not of the first one.
func TestValidateFleetYAML_ComparePatchOperationIndex(t *testing.T) {
	fy := &fleet.FleetYAML{
		BundleSpec: fleet.BundleSpec{
			BundleDeploymentOptions: fleet.BundleDeploymentOptions{
				Diff: &fleet.DiffOptions{
					ComparePatches: []fleet.ComparePatch{
						{Name: "first", Operations: []fleet.Operation{{Op: "ignore"}}},
						{Name: "second", Operations: []fleet.Operation{{Op: "remove", Path: "/spec/replicas"}}},
						{Name: "third", Operations: []fleet.Operation{{Op: "remove"}, {Op: "bogus"}}},
					},
				},
			},
		},
	}

	err := validateFleetYAML(fy)
	if err == nil {
		t.Fatal("validateFleetYAML() expected an error, got nil")
	}

	expected := `diff.comparePatches[2].operations[1].op: unsupported operation "bogus"`
	if !strings.Contains(err.Error(), expected) {
		t.Errorf("validateFleetYAML() error = %q, expected to contain %q", err.Error(), expected)
	}
}

// diffWithPatchPaths builds a diff with one comparePatch whose operations use the
// given paths, all with a "remove" op so that the path rule is what decides.
func diffWithPatchPaths(paths ...string) *fleet.DiffOptions {
	patch := fleet.ComparePatch{Name: "my-app"}
	for _, p := range paths {
		patch.Operations = append(patch.Operations, fleet.Operation{Op: "remove", Path: p})
	}

	return &fleet.DiffOptions{ComparePatches: []fleet.ComparePatch{patch}}
}

// diffWithJSONPointers builds a diff with one comparePatch carrying the given
// jsonPointers and no operations.
func diffWithJSONPointers(pointers ...string) *fleet.DiffOptions {
	return &fleet.DiffOptions{
		ComparePatches: []fleet.ComparePatch{{Name: "my-app", JsonPointers: pointers}},
	}
}

func TestValidateFleetYAML_ValidComparePatchOperationPaths(t *testing.T) {
	tests := []struct {
		name string
		diff *fleet.DiffOptions
	}{
		{name: "no diff at all", diff: nil},
		{name: "patch without operations", diff: diffWithPatchPaths()},
		{name: "simple pointer", diff: diffWithPatchPaths("/spec/replicas")},
		{name: "root child", diff: diffWithPatchPaths("/metadata")},
		{name: "array index", diff: diffWithPatchPaths("/spec/containers/0/image")},
		{name: "several operations", diff: diffWithPatchPaths("/spec/a", "/spec/b")},
		{
			// json-patch rewrites ~0 and ~1 and leaves any other ~ sequence
			// literal, so these address real keys and must keep working.
			name: "escaped tilde and slash",
			diff: diffWithPatchPaths("/spec/a~0b", "/spec/c~1d"),
		},
		{
			name: "tilde which is not an RFC 6901 escape",
			diff: diffWithPatchPaths("/spec/a~2b", "/spec/trailing~"),
		},
		{
			// A path which does not resolve on the live object is a runtime
			// concern, not a syntax one, and stays accepted.
			name: "pointer to a field which may not exist",
			diff: diffWithPatchPaths("/spec/doesNotExist"),
		},
		{
			// "ignore" drops the whole resource and never reads the path, so
			// whatever is written there cannot break anything.
			name: "ignore operation without a path",
			diff: &fleet.DiffOptions{
				ComparePatches: []fleet.ComparePatch{{
					Name:       "my-app",
					Operations: []fleet.Operation{{Op: "ignore"}},
				}},
			},
		},
		{
			name: "ignore operation with a malformed path",
			diff: &fleet.DiffOptions{
				ComparePatches: []fleet.ComparePatch{{
					Name:       "my-app",
					Operations: []fleet.Operation{{Op: "ignore", Path: "spec.replicas"}},
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: tt.diff,
					},
				},
			}

			if err := validateFleetYAML(fy); err != nil {
				t.Errorf("validateFleetYAML() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateFleetYAML_InvalidComparePatchOperationPaths(t *testing.T) {
	tests := []struct {
		name          string
		diff          *fleet.DiffOptions
		expectedError string
	}{
		{
			// The common mistake: a Kubernetes field path instead of a pointer.
			name:          "dotted field path",
			diff:          diffWithPatchPaths("spec.replicas"),
			expectedError: `diff.comparePatches[0].operations[0].path: invalid JSON pointer "spec.replicas": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name:          "single segment without a slash",
			diff:          diffWithPatchPaths("spec"),
			expectedError: `diff.comparePatches[0].operations[0].path: invalid JSON pointer "spec": must start with "/", e.g. "/spec"`,
		},
		{
			// Path is marshalled with omitempty, so an empty one reaches
			// json-patch as a missing "path" field and always fails.
			name:          "empty path",
			diff:          diffWithPatchPaths(""),
			expectedError: `diff.comparePatches[0].operations[0].path: invalid JSON pointer "": must not be empty`,
		},
		{
			name:          "second operation is the offending one",
			diff:          diffWithPatchPaths("/spec/replicas", "spec.template"),
			expectedError: `diff.comparePatches[0].operations[1].path: invalid JSON pointer "spec.template": must start with "/", e.g. "/spec/template"`,
		},
		{
			name: "non-ignore operation other than remove",
			diff: &fleet.DiffOptions{
				ComparePatches: []fleet.ComparePatch{{
					Name:       "my-app",
					Operations: []fleet.Operation{{Op: "replace", Path: "spec.type", Value: "ClusterIP"}},
				}},
			},
			expectedError: `diff.comparePatches[0].operations[0].path: invalid JSON pointer "spec.type"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: tt.diff,
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

func TestValidateFleetYAML_ComparePatchOperationPathOptionSites(t *testing.T) {
	tests := []struct {
		name          string
		fy            *fleet.FleetYAML
		expectedError string
	}{
		{
			name: "bundle level",
			fy: &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: diffWithPatchPaths("spec.replicas"),
					},
				},
			},
			expectedError: `diff.comparePatches[0].operations[0].path: invalid JSON pointer "spec.replicas"`,
		},
		{
			name: "targets",
			fy: &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					Targets: []fleet.BundleTarget{
						{Name: "first"},
						{
							Name: "second",
							BundleDeploymentOptions: fleet.BundleDeploymentOptions{
								Diff: diffWithPatchPaths("/spec/ok", "spec.bad"),
							},
						},
					},
				},
			},
			expectedError: `targets[1].diff.comparePatches[0].operations[1].path: invalid JSON pointer "spec.bad"`,
		},
		{
			name: "targetCustomizations",
			fy: &fleet.FleetYAML{
				TargetCustomizations: []fleet.BundleTarget{
					{Name: "first"},
					{
						Name: "second",
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Diff: diffWithPatchPaths(""),
						},
					},
				},
			},
			expectedError: `targetCustomizations[1].diff.comparePatches[0].operations[0].path: invalid JSON pointer ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFleetYAML(tt.fy)
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

func TestValidateFleetYAML_ValidComparePatchJSONPointers(t *testing.T) {
	tests := []struct {
		name string
		diff *fleet.DiffOptions
	}{
		{name: "no diff at all", diff: nil},
		{name: "patch without pointers", diff: diffWithJSONPointers()},
		{name: "simple pointer", diff: diffWithJSONPointers("/spec/replicas")},
		{name: "several pointers", diff: diffWithJSONPointers("/spec/replicas", "/metadata/labels")},
		{name: "array index", diff: diffWithJSONPointers("/spec/containers/0/image")},
		{name: "escaped tilde and slash", diff: diffWithJSONPointers("/spec/a~0b", "/spec/c~1d")},
		{name: "tilde which is not an RFC 6901 escape", diff: diffWithJSONPointers("/spec/a~2b")},
		{name: "pointer to a field which may not exist", diff: diffWithJSONPointers("/spec/doesNotExist")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: tt.diff,
					},
				},
			}

			if err := validateFleetYAML(fy); err != nil {
				t.Errorf("validateFleetYAML() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateFleetYAML_InvalidComparePatchJSONPointers(t *testing.T) {
	tests := []struct {
		name          string
		diff          *fleet.DiffOptions
		expectedError string
	}{
		{
			name:          "dotted field path",
			diff:          diffWithJSONPointers("spec.replicas"),
			expectedError: `diff.comparePatches[0].jsonPointers[0]: invalid JSON pointer "spec.replicas": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name:          "empty pointer",
			diff:          diffWithJSONPointers(""),
			expectedError: `diff.comparePatches[0].jsonPointers[0]: invalid JSON pointer "": must not be empty`,
		},
		{
			name:          "second pointer is the offending one",
			diff:          diffWithJSONPointers("/spec/replicas", "spec.template"),
			expectedError: `diff.comparePatches[0].jsonPointers[1]: invalid JSON pointer "spec.template"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fy := &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: tt.diff,
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

func TestValidateFleetYAML_ComparePatchJSONPointerOptionSites(t *testing.T) {
	tests := []struct {
		name          string
		fy            *fleet.FleetYAML
		expectedError string
	}{
		{
			name: "bundle level",
			fy: &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Diff: diffWithJSONPointers("spec.replicas"),
					},
				},
			},
			expectedError: `diff.comparePatches[0].jsonPointers[0]: invalid JSON pointer "spec.replicas"`,
		},
		{
			name: "targets",
			fy: &fleet.FleetYAML{
				BundleSpec: fleet.BundleSpec{
					Targets: []fleet.BundleTarget{
						{Name: "first"},
						{
							Name: "second",
							BundleDeploymentOptions: fleet.BundleDeploymentOptions{
								Diff: diffWithJSONPointers("/spec/ok", "spec.bad"),
							},
						},
					},
				},
			},
			expectedError: `targets[1].diff.comparePatches[0].jsonPointers[1]: invalid JSON pointer "spec.bad"`,
		},
		{
			name: "targetCustomizations",
			fy: &fleet.FleetYAML{
				TargetCustomizations: []fleet.BundleTarget{
					{Name: "first"},
					{
						Name: "second",
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Diff: diffWithJSONPointers(""),
						},
					},
				},
			},
			expectedError: `targetCustomizations[1].diff.comparePatches[0].jsonPointers[0]: invalid JSON pointer ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFleetYAML(tt.fy)
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

// TestValidateFleetYAML_ComparePatchPointerIndex checks that the reported index is
// the index of the offending patch, not of the first one.
func TestValidateFleetYAML_ComparePatchPointerIndex(t *testing.T) {
	fy := &fleet.FleetYAML{
		BundleSpec: fleet.BundleSpec{
			BundleDeploymentOptions: fleet.BundleDeploymentOptions{
				Diff: &fleet.DiffOptions{
					ComparePatches: []fleet.ComparePatch{
						{Name: "first", Operations: []fleet.Operation{{Op: "ignore"}}},
						{Name: "second", JsonPointers: []string{"/spec/replicas"}},
						{Name: "third", JsonPointers: []string{"/ok", "not-a-pointer"}},
					},
				},
			},
		},
	}

	err := validateFleetYAML(fy)
	if err == nil {
		t.Fatal("validateFleetYAML() expected an error, got nil")
	}

	expected := `diff.comparePatches[2].jsonPointers[1]: invalid JSON pointer "not-a-pointer"`
	if !strings.Contains(err.Error(), expected) {
		t.Errorf("validateFleetYAML() error = %q, expected to contain %q", err.Error(), expected)
	}
}

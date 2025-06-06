package target

import (
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// availableTarget returns a Target that is considered available.
func availableTarget() *Target {
	return &Target{
		Deployment: &fleet.BundleDeployment{
			Spec: fleet.BundleDeploymentSpec{
				DeploymentID:       "id",
				StagedDeploymentID: "id",
			},
			Status: fleet.BundleDeploymentStatus{
				AppliedDeploymentID: "id",
				Ready:               true,
			},
		},
		DeploymentID: "id",
	}
}

// unavailableTargetMismatchedID returns a Target that is considered
// unavailable due to a mismatched ID.
func unavailableTargetMismatchedID() *Target {
	return &Target{
		Deployment: &fleet.BundleDeployment{
			Spec: fleet.BundleDeploymentSpec{
				DeploymentID:       "id",
				StagedDeploymentID: "id",
			},
			Status: fleet.BundleDeploymentStatus{
				AppliedDeploymentID: "off-id",
				Ready:               true,
			},
		},
		DeploymentID: "id",
	}
}

// unavailableTargetNonReady returns a Target that is considered
// unavailable due to not being ready.
func unavailableTargetNonReady() *Target {
	return &Target{
		Deployment: &fleet.BundleDeployment{
			Spec: fleet.BundleDeploymentSpec{
				DeploymentID:       "id",
				StagedDeploymentID: "id",
			},
			Status: fleet.BundleDeploymentStatus{
				AppliedDeploymentID: "id",
				Ready:               false,
			},
		},
		DeploymentID: "id",
	}
}

func targetWithRolloutStrategy(target *Target, rolloutStrategy fleet.RolloutStrategy) *Target {
	if target.Bundle == nil {
		target.Bundle = &fleet.Bundle{}
	}
	target.Bundle.Spec.RolloutStrategy = &rolloutStrategy
	return target
}

func Test_limit(t *testing.T) {
	tests := []struct {
		name    string
		count   int
		val     []*intstr.IntOrString
		want    int
		wantErr bool
	}{
		{
			name:  "fixed value below count",
			count: 10,
			val: []*intstr.IntOrString{
				{IntVal: 5},
			},
			want: 5,
		},
		{
			name:  "fixed value above count",
			count: 10,
			val: []*intstr.IntOrString{
				{IntVal: 15},
			},
			want: 15,
		},
		{
			name:  "with value with zero count",
			count: 0,
			val: []*intstr.IntOrString{
				{IntVal: 15},
			},
			want: 1,
		},
		{
			name:  "fixed value with negative count",
			count: -15,
			val: []*intstr.IntOrString{
				{IntVal: 15},
			},
			want: 1,
		},
		{
			name:  "two fixed values should take the first one",
			count: 10,
			val: []*intstr.IntOrString{
				{IntVal: 5},
				{IntVal: 15},
			},
			want: 5,
		},
		{
			name:  "two fixed values should ignore nil",
			count: 10,
			val: []*intstr.IntOrString{
				nil,
				{IntVal: 15},
			},
			want: 15,
		},
		{
			name:  "percent value 50",
			count: 10,
			val: []*intstr.IntOrString{
				{Type: intstr.String, StrVal: "50%"},
			},
			want: 5,
		},
		{
			name:  "percent value 10",
			count: 10,
			val: []*intstr.IntOrString{
				{Type: intstr.String, StrVal: "10%"},
			},
			want: 1,
		},
		{
			name:  "negative percent value",
			count: 10,
			val: []*intstr.IntOrString{
				{Type: intstr.String, StrVal: "-10%"},
			},
			want: 1,
		},
		{
			name:  "percent value 10 with count 5",
			count: 5,
			val: []*intstr.IntOrString{
				{Type: intstr.String, StrVal: "10%"},
			},
			want: 1,
		},
		{
			name:  "no value should match count",
			count: 50,
			val:   []*intstr.IntOrString{},
			want:  50,
		},
		{
			name:  "percentage below 1 should return 1",
			count: 5,
			val: []*intstr.IntOrString{
				{Type: intstr.String, StrVal: "1%"},
			},
			want: 1,
		},
		{
			name:  "percentages are always rounded down",
			count: 10,
			val: []*intstr.IntOrString{
				{Type: intstr.String, StrVal: "49%"},
			},
			want: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := limit(tt.count, tt.val...)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("limit() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("limit() succeeded unexpectedly")
			}
			if got != tt.want {
				t.Errorf("limit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_isUnavailable(t *testing.T) {
	tests := []struct {
		name   string
		target *fleet.BundleDeployment
		want   bool
	}{
		{
			name:   "empty target should not be unavailable",
			target: nil,
			want:   false,
		},
		{
			name: "ready but AppliedDeploymentID does not match DeploymentID",
			target: &fleet.BundleDeployment{
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "123",
				},
				Status: fleet.BundleDeploymentStatus{
					AppliedDeploymentID: "456",
					Ready:               true,
				},
			},
			want: true,
		},
		{
			name: "ready and AppliedDeploymentID does match DeploymentID",
			target: &fleet.BundleDeployment{
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "123",
				},
				Status: fleet.BundleDeploymentStatus{
					AppliedDeploymentID: "123",
					Ready:               true,
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnavailable(tt.target)
			if got != tt.want {
				t.Errorf("isAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_upToDate(t *testing.T) {
	tests := []struct {
		name   string
		target *Target
		want   bool
	}{
		{
			name:   "is not up-to-date if deployment is nil",
			target: &Target{},
			want:   false,
		},
		{
			name: "is not up-to-date if .Spec.StagedDeploymentID does not match target.DeploymentID",
			target: &Target{
				Deployment: &fleet.BundleDeployment{
					Spec: fleet.BundleDeploymentSpec{
						DeploymentID:       "id",
						StagedDeploymentID: "off-id",
					},
					Status: fleet.BundleDeploymentStatus{
						AppliedDeploymentID: "id",
					},
				},
				DeploymentID: "id",
			},
			want: false,
		},
		{
			name: "is not up-to-date if .Spec.DeploymentID does not match target.DeploymentID",
			target: &Target{
				Deployment: &fleet.BundleDeployment{
					Spec: fleet.BundleDeploymentSpec{
						DeploymentID:       "off-id",
						StagedDeploymentID: "id",
					},
					Status: fleet.BundleDeploymentStatus{
						AppliedDeploymentID: "id",
					},
				},
				DeploymentID: "id",
			},
			want: false,
		},
		{
			name: "is not up-to-date if .Status.AppliedDeploymentID does not match target.DeploymentID",
			target: &Target{
				Deployment: &fleet.BundleDeployment{
					Spec: fleet.BundleDeploymentSpec{
						DeploymentID:       "id",
						StagedDeploymentID: "id",
					},
					Status: fleet.BundleDeploymentStatus{
						AppliedDeploymentID: "off-id",
					},
				},
				DeploymentID: "id",
			},
			want: false,
		},
		{
			name: "is up-to-date",
			target: &Target{
				Deployment: &fleet.BundleDeployment{
					Spec: fleet.BundleDeploymentSpec{
						DeploymentID:       "id",
						StagedDeploymentID: "id",
					},
					Status: fleet.BundleDeploymentStatus{
						AppliedDeploymentID: "id",
					},
				},
				DeploymentID: "id",
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upToDate(tt.target)
			if got != tt.want {
				t.Errorf("upToDate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_updateStatusAndCheckUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		status  *fleet.PartitionStatus
		targets []*Target
		want    bool
	}{
		{
			name: "should be available if all targets are available",
			status: &fleet.PartitionStatus{
				MaxUnavailable: 0,
			},
			targets: []*Target{
				availableTarget(),
				availableTarget(),
			},
			want: false,
		},
		{
			name: "should be unavailable if one target is unavailable but 0 can be",
			status: &fleet.PartitionStatus{
				MaxUnavailable: 0,
			},
			targets: []*Target{
				availableTarget(),
				availableTarget(),
				unavailableTargetMismatchedID(),
			},
			want: true,
		},
		{
			name: "should be available if max unavailable is 1 and one target is unavailable",
			status: &fleet.PartitionStatus{
				MaxUnavailable: 1,
			},
			targets: []*Target{
				availableTarget(),
				availableTarget(),
				unavailableTargetMismatchedID(),
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := updateStatusAndCheckUnavailable(tt.status, tt.targets)
			if got != tt.want {
				t.Errorf("updateStatusUnavailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		targets []*Target
		want    int
	}{
		{
			name: "should correctly count unavailable targets",
			targets: []*Target{
				availableTarget(),
				availableTarget(),
				unavailableTargetMismatchedID(),
				unavailableTargetNonReady(),
				availableTarget(),
				{}, // Deployment is nil
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Unavailable(tt.targets)
			if got != tt.want {
				t.Errorf("Unavailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMaxUnavailable tests the MaxUnavailable function to ensure it correctly
// calculates the maximum number of unavailable targets based on the provided
// rollout strategy.
func TestMaxUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		targets []*Target
		want    int
		wantErr bool
	}{
		{
			name: "zero should be zero",
			targets: []*Target{
				targetWithRolloutStrategy(&Target{}, fleet.RolloutStrategy{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 0},
				}),
			},
			want: 0,
		},
		{
			name: "percentage that leads to less than 1 max unavailable should return 1",
			targets: []*Target{
				targetWithRolloutStrategy(unavailableTargetNonReady(), fleet.RolloutStrategy{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "1%"},
				}),
				unavailableTargetNonReady(),
				unavailableTargetNonReady(),
				unavailableTargetNonReady(),
			},
			want: 1,
		},
		{
			name: "25% of 4 is 1",
			targets: []*Target{
				targetWithRolloutStrategy(unavailableTargetNonReady(), fleet.RolloutStrategy{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
				}),
				unavailableTargetNonReady(),
				unavailableTargetNonReady(),
				unavailableTargetNonReady(),
			},
			want: 1,
		},
		{
			name: "49% of 4 is 1",
			targets: []*Target{
				targetWithRolloutStrategy(unavailableTargetNonReady(), fleet.RolloutStrategy{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "49%"},
				}),
				unavailableTargetNonReady(),
				unavailableTargetNonReady(),
				unavailableTargetNonReady(),
			},
			want: 1,
		},
		{
			name: "50% of 4 is 2",
			targets: []*Target{
				targetWithRolloutStrategy(unavailableTargetNonReady(), fleet.RolloutStrategy{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
				}),
				unavailableTargetNonReady(),
				unavailableTargetNonReady(),
				unavailableTargetNonReady(),
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := MaxUnavailable(tt.targets)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("MaxUnavailable() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("MaxUnavailable() succeeded unexpectedly")
			}
			if got != tt.want {
				t.Errorf("MaxUnavailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

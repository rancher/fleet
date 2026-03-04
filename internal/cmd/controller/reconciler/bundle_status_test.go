package reconciler

import (
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/target"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func intPtr(i int) *int {
	return &i
}

func Test_resetStatus_MaxNew(t *testing.T) {
	tests := []struct {
		name       string
		rollout    *fleet.RolloutStrategy
		wantMaxNew int
	}{
		{
			name:       "nil rollout strategy uses default maxNew",
			rollout:    nil,
			wantMaxNew: defaultMaxNew,
		},
		{
			name:       "rollout strategy without MaxNew uses default",
			rollout:    &fleet.RolloutStrategy{},
			wantMaxNew: defaultMaxNew,
		},
		{
			name: "rollout strategy with MaxNew configured uses it",
			rollout: &fleet.RolloutStrategy{
				MaxNew: intPtr(100),
			},
			wantMaxNew: 100,
		},
		{
			name: "rollout strategy with MaxNew of 0 prevents new deployments",
			rollout: &fleet.RolloutStrategy{
				MaxNew: intPtr(0),
			},
			wantMaxNew: 0,
		},
		{
			name: "rollout strategy with MaxNew larger than default is accepted",
			rollout: &fleet.RolloutStrategy{
				MaxNew: intPtr(200),
			},
			wantMaxNew: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &fleet.BundleStatus{}
			if err := resetStatus(status, []*target.Target{}, tt.rollout); err != nil {
				t.Fatalf("resetStatus() failed: %v", err)
			}
			if status.MaxNew != tt.wantMaxNew {
				t.Errorf("MaxNew = %d, want %d", status.MaxNew, tt.wantMaxNew)
			}
		})
	}
}

package cli

import (
	"strconv"
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/target"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newTargetsWithoutDeployments creates targets that share a single bundle
// pointer, matching what the real target builder produces.
func newTargetsWithoutDeployments(bundle *fleet.Bundle, count int) []*target.Target {
	targets := make([]*target.Target, count)
	for i := range count {
		targets[i] = &target.Target{
			Cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "cluster-" + strconv.Itoa(i+1),
				},
			},
			Bundle:       bundle,
			DeploymentID: "deployment-" + strconv.Itoa(i+1),
		}
	}
	return targets
}

func Test_stageAllTargets(t *testing.T) {
	tests := []struct {
		name        string
		targetCount int
	}{
		{
			name:        "stages all targets when count exceeds default maxNew",
			targetCount: 100,
		},
		{
			name:        "stages all targets when count is below default maxNew",
			targetCount: 10,
		},
		{
			name:        "no targets produces no deployments",
			targetCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "bundle-1"}}
			targets := newTargetsWithoutDeployments(bundle, tt.targetCount)

			if err := stageAllTargets(bundle, targets); err != nil {
				t.Fatalf("stageAllTargets() failed: %v", err)
			}

			deploymentCount := 0
			for _, tgt := range targets {
				if tgt.Deployment != nil {
					deploymentCount++
				}
			}
			if deploymentCount != tt.targetCount {
				t.Errorf("staged %d deployments, want %d", deploymentCount, tt.targetCount)
			}
		})
	}
}

func Test_stageAllTargets_overwritesExistingMaxNew(t *testing.T) {
	// MaxNew=2 is less than the target count (5); if stageAllTargets does not
	// overwrite it, UpdatePartitions would only stage 2 deployments.
	two := 2
	bundle := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "bundle-1"}}
	bundle.Spec.RolloutStrategy = &fleet.RolloutStrategy{MaxNew: &two}
	targets := newTargetsWithoutDeployments(bundle, 5)

	if err := stageAllTargets(bundle, targets); err != nil {
		t.Fatalf("stageAllTargets() failed: %v", err)
	}

	deploymentCount := 0
	for _, tgt := range targets {
		if tgt.Deployment != nil {
			deploymentCount++
		}
	}
	if deploymentCount != 5 {
		t.Errorf("staged %d deployments, want 5", deploymentCount)
	}
}

func Test_stageAllTargets_preservesExistingRolloutStrategy(t *testing.T) {
	ten := 10
	existing := &fleet.RolloutStrategy{
		AutoPartitionThreshold: &ten,
	}
	bundle := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "bundle-1"}}
	bundle.Spec.RolloutStrategy = existing
	targets := newTargetsWithoutDeployments(bundle, 5)

	if err := stageAllTargets(bundle, targets); err != nil {
		t.Fatalf("stageAllTargets() failed: %v", err)
	}

	if bundle.Spec.RolloutStrategy.AutoPartitionThreshold == nil || *bundle.Spec.RolloutStrategy.AutoPartitionThreshold != 10 {
		t.Error("existing rollout strategy fields were overwritten")
	}
}

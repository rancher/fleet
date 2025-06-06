package target

import (
	"fmt"
	"strconv"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// createTargets creates a slice of targets with sequentially numbered clusters
// and bundles. Both values start and stop are inclusive, meaning the targets
// will be created from start to stop.
func createTargets(start, stop int) []*Target {
	targets := make([]*Target, stop-start+1)
	for i := range stop - start + 1 {
		targets[i] = &Target{
			Cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "cluster-" + strconv.Itoa(start),
				},
			},
			Bundle: &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name: "bundle-" + strconv.Itoa(start),
				},
			},
			Deployment: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       fleet.BundleDeploymentSpec{},
				Status:     fleet.BundleDeploymentStatus{},
			},
			DeploymentID: "deployment-" + strconv.Itoa(start),
		}
		start++
	}
	if start != stop+1 {
		panic(fmt.Sprintf("createTargets: start and stop values are not equal: start: %d, stop: %d", start, stop))
	}
	return targets
}

func Test_createTargets(t *testing.T) {
	tests := []struct {
		name        string
		start, stop int
		want        []*Target
	}{
		{
			name:  "start and stop should be inclusive",
			start: 1,
			stop:  5,
			want: []*Target{
				{DeploymentID: "deployment-1"},
				{DeploymentID: "deployment-2"},
				{DeploymentID: "deployment-3"},
				{DeploymentID: "deployment-4"},
				{DeploymentID: "deployment-5"},
			},
		},
	}
	for _, tt := range tests {
		got := createTargets(tt.start, tt.stop)
		if err := targetsEqual(got, tt.want); err != nil {
			t.Errorf("createTargets(%d, %d): %v", tt.start, tt.stop, err)
		}
	}
}

func withCluster(targets []*Target, cluster *fleet.Cluster) {
	for _, target := range targets {
		target.Cluster = cluster
	}
}

func withClusterGroup(targets []*Target, clusterGroup *fleet.ClusterGroup) {
	for _, target := range targets {
		target.ClusterGroups = append(target.ClusterGroups, clusterGroup)
	}
}

func Test_withClusterGroup(t *testing.T) {
	clusterGroup := &fleet.ClusterGroup{}
	target1 := &Target{}
	target2 := &Target{}
	targets := []*Target{target1, target2}

	withClusterGroup(targets, clusterGroup)

	for _, target := range targets {
		if len(target.ClusterGroups) != 1 || target.ClusterGroups[0] != clusterGroup {
			t.Errorf("expected cluster group to be appended to target, got %+v", target.ClusterGroups)
		}
	}
}

func targetsEqual(got, want []*Target) error {
	if len(want) != len(got) {
		return fmt.Errorf("targets have different lengths: got %d but want %d", len(got), len(want))
	}

	for i := range want {
		if want[i].DeploymentID != got[i].DeploymentID {
			return fmt.Errorf("target %d has different deployment IDs: got %v but want %v", i, got[i], want[i])
		}
	}

	return nil
}

// partitionsEqual compares two slices of partitions for equality. It ignores
// the status of the partitions.
func partitionsEqual(got, want []partition) error {
	if len(got) != len(want) {
		return fmt.Errorf("partitions have different lengths: got %d but want %d", len(got), len(want))
	}
	for i := range want {
		if err := targetsEqual(got[i].Targets, want[i].Targets); err != nil {
			return fmt.Errorf("partition %d has different targets: %v", i, err)
		}
	}
	return nil
}

func Test_autoPartition(t *testing.T) {
	tests := []struct {
		name    string
		rollout *fleet.RolloutStrategy
		targets []*Target
		want    []partition
		wantErr bool
	}{
		{
			name: "less than 200 targets should all be in one partition",
			rollout: &fleet.RolloutStrategy{
				AutoPartitionSize: &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
				MaxUnavailable:    &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
			},
			targets: createTargets(1, 199),
			want: []partition{
				{Targets: createTargets(1, 199)},
			},
		},
		{
			name:    "with 200 targets and above, we expect 4 partitions with a default of 25% for partition size",
			rollout: &fleet.RolloutStrategy{},
			targets: createTargets(1, 200),
			want: []partition{
				{Targets: createTargets(1, 50)},
				{Targets: createTargets(51, 100)},
				{Targets: createTargets(101, 150)},
				{Targets: createTargets(151, 200)},
			},
		},
		{
			name: "rest ends up in a separate partition",
			rollout: &fleet.RolloutStrategy{
				AutoPartitionSize: &intstr.IntOrString{Type: intstr.String, StrVal: "49%"},
			},
			targets: createTargets(1, 1000),
			want: []partition{
				{Targets: createTargets(1, 490)},
				{Targets: createTargets(491, 980)},
				{Targets: createTargets(981, 1000)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := autoPartition(tt.rollout, tt.targets)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("autoPartition() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("autoPartition() succeeded unexpectedly")
			}

			if err := partitionsEqual(got, tt.want); err != nil {
				t.Errorf("autoPartition(): %v", err)
			}
		})
	}
}

func Test_manualPartition(t *testing.T) {
	tests := []struct {
		name      string
		rollout   *fleet.RolloutStrategy
		targets   []*Target
		targetsFn func() []*Target
		want      []partition
		wantErr   bool
	}{
		{
			name: "should match cluster names",
			rollout: &fleet.RolloutStrategy{
				Partitions: []fleet.Partition{
					{
						Name:        "Partition 1",
						ClusterName: "cluster-1",
					},
					{
						Name:        "Partition 2",
						ClusterName: "cluster-2",
					},
				},
			},
			// targets: createTargets(1, 4),
			targetsFn: func() []*Target {
				cluster1 := &fleet.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-1",
					},
				}
				cluster2 := &fleet.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-2",
					},
				}
				targets := createTargets(1, 4)
				withCluster(targets[0:2], cluster1)
				withCluster(targets[2:4], cluster2)
				return targets
			},
			want: []partition{
				{
					Targets: createTargets(1, 2),
				},
				{
					Targets: createTargets(3, 4),
				},
			},
		},
		{
			name: "should match cluster groups",
			rollout: &fleet.RolloutStrategy{
				Partitions: []fleet.Partition{
					{
						Name:         "Partition 1",
						ClusterGroup: "group-1",
					},
					{
						Name:         "Partition 2",
						ClusterGroup: "group-2",
					},
				},
			},
			targetsFn: func() []*Target {
				targets := createTargets(1, 4)
				cluster1 := &fleet.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-1",
					},
				}
				cluster2 := &fleet.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-2",
					},
				}
				withCluster(targets[0:2], cluster1)
				withCluster(targets[2:4], cluster2)
				withClusterGroup(targets[0:2], &fleet.ClusterGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "group-1",
					},
					Spec: fleet.ClusterGroupSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"group": "group-1"},
						},
					},
				})
				withClusterGroup(targets[2:4], &fleet.ClusterGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "group-2",
					},
					Spec: fleet.ClusterGroupSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"group": "group-2"},
						},
					},
				})
				return targets
			},
			want: []partition{
				{Targets: createTargets(1, 2)},
				{Targets: createTargets(3, 4)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var targets []*Target
			if len(tt.targets) > 0 {
				targets = tt.targets
			} else {
				targets = tt.targetsFn()
			}
			got, gotErr := manualPartition(tt.rollout, targets)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("manualPartition() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("manualPartition() succeeded unexpectedly")
			}
			if err := partitionsEqual(got, tt.want); err != nil {
				fmt.Printf("got: %+v\n", got)
				fmt.Printf("want: %+v\n", tt.want)
				t.Errorf("manualPartition(): %v", err)
			}
		})
	}
}

package target

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/match"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type Partition struct {
	Status  fleet.PartitionStatus
	Targets []*Target
}

func Partitions(targets []*Target) ([]Partition, error) {
	rollout := getRollout(targets)
	if len(rollout.Partitions) == 0 {
		return autoPartition(rollout, targets)
	}

	return manualPartition(rollout, targets)
}

func manualPartition(rollout *fleet.RolloutStrategy, targets []*Target) ([]Partition, error) {
	var (
		partitions []Partition
	)

	for _, partitionDef := range rollout.Partitions {
		matcher, err := match.NewClusterMatcher(partitionDef.ClusterGroup, partitionDef.ClusterGroupSelector, partitionDef.ClusterSelector)
		if err != nil {
			return nil, err
		}

		var partitionTargets []*Target
	targetLoop:
		for _, target := range targets {
			for _, cg := range target.ClusterGroups {
				if matcher.Match(cg.Name, cg.Labels, target.Cluster.Labels) {
					partitionTargets = append(partitionTargets, target)
					continue targetLoop
				}
			}
		}

		partitions, err = appendPartition(partitions, partitionDef.Name, partitionTargets, partitionDef.MaxUnavailable, rollout.MaxUnavailable)
		if err != nil {
			return nil, err
		}
	}

	return partitions, nil
}

func appendPartition(partitions []Partition, name string, partitionTargets []*Target, maxUnavailable ...*intstr.IntOrString) ([]Partition, error) {
	maxUnavailableValue, err := Limit(len(partitionTargets), maxUnavailable...)
	if err != nil {
		return nil, err
	}
	return append(partitions, Partition{
		Status: fleet.PartitionStatus{
			Name:           name,
			Count:          len(partitionTargets),
			MaxUnavailable: maxUnavailableValue,
			Unavailable:    Unavailable(partitionTargets),
			Summary:        Summary(partitionTargets),
		},
		Targets: partitionTargets,
	}), nil
}

func autoPartition(rollout *fleet.RolloutStrategy, targets []*Target) ([]Partition, error) {
	// if auto is disabled
	if rollout.AutoPartitionSize != nil && rollout.AutoPartitionSize.Type == intstr.Int &&
		rollout.AutoPartitionSize.IntVal <= 0 {
		return appendPartition(nil, "All", targets, rollout.MaxUnavailable)
	}

	maxSize, err := Limit(len(targets), rollout.AutoPartitionSize, &defAutoPartitionSize)
	if err != nil {
		return nil, err
	}

	var (
		partitions []Partition
		offset     = 0
	)

	for {
		if len(targets) == 0 {
			return partitions, nil
		}
		end := maxSize
		if len(targets) < maxSize {
			end = len(targets)
		}

		partitionTargets := targets[:end]
		name := fmt.Sprintf("Partition %d - %d", offset, offset+end)

		partitions, err = appendPartition(partitions, name, partitionTargets, rollout.MaxUnavailable)
		if err != nil {
			return nil, err
		}

		// setup next loop
		targets = targets[end:]
		offset += end
	}
}

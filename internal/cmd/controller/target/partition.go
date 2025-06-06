package target

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type partition struct {
	Status  fleet.PartitionStatus
	Targets []*Target
}

// UpdatePartitions recomputes status, including partitions, from data in allTargets.
// It creates Deployments in allTargets if they are missing.
// It updates Deployments in allTargets if they are out of sync (DeploymentID != StagedDeploymentID).
func UpdatePartitions(status *fleet.BundleStatus, allTargets []*Target) (err error) {
	partitions, err := partitions(allTargets)
	if err != nil {
		return err
	}

	status.UnavailablePartitions = 0
	status.MaxUnavailablePartitions, err = maxUnavailablePartitions(partitions, allTargets)
	if err != nil {
		return err
	}

	for _, partition := range partitions {
		partition := partition // fix gosec warning regarding "Implicit memory aliasing in for loop"
		for _, target := range partition.Targets {
			// for a new bundledeployment, only stage the first maxNew (50) targets
			if target.Deployment == nil && status.NewlyCreated < status.MaxNew {
				status.NewlyCreated++
				target.Deployment = &fleet.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      target.Bundle.Name,
						Namespace: target.Cluster.Status.Namespace,
						Labels:    target.BundleDeploymentLabels(target.Cluster.Namespace, target.Cluster.Name),
					},
				}
			}
			// stage targets that have a Deployment struct
			if target.Deployment != nil {
				// NOTE merged options from targets.Targets() are set to be staged
				target.Deployment.Spec.StagedOptions = target.Options
				target.Deployment.Spec.StagedDeploymentID = target.DeploymentID
			}
		}

		for _, currentTarget := range partition.Targets {
			// NOTE this will propagate the staged, merged options to the current deployment
			updateTarget(currentTarget, status, &partition.Status)
		}

		if updateStatusAndCheckUnavailable(&partition.Status, partition.Targets) {
			status.UnavailablePartitions++
		}

		if status.UnavailablePartitions > status.MaxUnavailablePartitions {
			break
		}
	}

	for _, partition := range partitions {
		status.PartitionStatus = append(status.PartitionStatus, partition.Status)
	}

	return nil
}

// maxUnavailablePartitions returns the maximum number of unavailable partitions given the targets and partitions (pure function)
func maxUnavailablePartitions(partitions []partition, targets []*Target) (int, error) {
	rollout := getRollout(targets)
	return limit(len(partitions), rollout.MaxUnavailablePartitions, &defMaxUnavailablePartitions)
}

// updateTarget will update DeploymentID and Options for the target to the
// staging values, if it's in a deployable state
func updateTarget(t *Target, status *fleet.BundleStatus, partitionStatus *fleet.PartitionStatus) {
	if t.Deployment != nil &&
		// Not Paused
		!t.IsPaused() &&
		// Has been staged
		t.Deployment.Spec.StagedDeploymentID != "" &&
		// Is out of sync
		t.Deployment.Spec.DeploymentID != t.Deployment.Spec.StagedDeploymentID &&
		// Global max unavailable not reached
		(status.Unavailable < status.MaxUnavailable || isUnavailable(t.Deployment)) &&
		// Partition max unavailable not reached
		(partitionStatus.Unavailable < partitionStatus.MaxUnavailable || isUnavailable(t.Deployment)) {

		if !isUnavailable(t.Deployment) {
			// If this was previously available, now increment unavailable count. "Upgrading" is treated as unavailable.
			status.Unavailable++
			partitionStatus.Unavailable++
		}
		t.Deployment.Spec.DeploymentID = t.Deployment.Spec.StagedDeploymentID
		t.Deployment.Spec.Options = t.Deployment.Spec.StagedOptions
	}
}

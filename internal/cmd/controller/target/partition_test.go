package target

import (
	"testing"

	"github.com/stretchr/testify/assert"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// newStagedTarget builds a target whose deployment is already in sync with its
// staged deployment ID, i.e. the state in which the promotion in
// updateDeploymentFromStaged does not run.
func newStagedTarget(current, staged fleet.BundleDeploymentOptions) *Target {
	return &Target{
		Cluster: &fleet.Cluster{},
		Bundle:  &fleet.Bundle{},
		Deployment: &fleet.BundleDeployment{
			Spec: fleet.BundleDeploymentSpec{
				DeploymentID:       "manifest:hash",
				StagedDeploymentID: "manifest:hash",
				Options:            current,
				StagedOptions:      staged,
			},
		},
	}
}

// Namespace metadata is excluded from the DeploymentID hash, so a target that
// stops matching a customization keeps its old metadata unless it is synced
// explicitly: its DeploymentID never changes, so the promotion below never runs.
// This is what leaves namespace labels behind on deployments created before the
// metadata was scoped to matched targets.
func TestUpdateDeploymentFromStaged_SyncsNamespaceMetadata(t *testing.T) {
	a := assert.New(t)

	tgt := newStagedTarget(
		fleet.BundleDeploymentOptions{
			NamespaceLabels:      map[string]string{"tier": "critical"},
			NamespaceAnnotations: map[string]string{"owner": "prod"},
		},
		fleet.BundleDeploymentOptions{},
	)

	updateDeploymentFromStaged(tgt, &fleet.BundleStatus{}, &fleet.PartitionStatus{})

	a.Nil(tgt.Deployment.Spec.Options.NamespaceLabels)
	a.Nil(tgt.Deployment.Spec.Options.NamespaceAnnotations)
	a.Equal("manifest:hash", tgt.Deployment.Spec.DeploymentID)
}

// The staged metadata replaces the current one wholesale: keys are not merged,
// otherwise a key dropped from a customization would survive.
func TestUpdateDeploymentFromStaged_ReplacesNamespaceMetadata(t *testing.T) {
	a := assert.New(t)

	tgt := newStagedTarget(
		fleet.BundleDeploymentOptions{
			NamespaceLabels: map[string]string{"tier": "critical", "stale": "yes"},
		},
		fleet.BundleDeploymentOptions{
			NamespaceLabels: map[string]string{"tier": "standard"},
		},
	)

	updateDeploymentFromStaged(tgt, &fleet.BundleStatus{}, &fleet.PartitionStatus{})

	a.Equal(map[string]string{"tier": "standard"}, tgt.Deployment.Spec.Options.NamespaceLabels)
}

// Namespace metadata and diff options are synced independently: dropping a
// namespace label must propagate even when the diff options never change.
func TestUpdateDeploymentFromStaged_SyncsDiffAndNamespaceMetadataIndependently(t *testing.T) {
	a := assert.New(t)

	diff := &fleet.DiffOptions{
		ComparePatches: []fleet.ComparePatch{{Kind: "Service", Name: "svc"}},
	}

	tgt := newStagedTarget(
		fleet.BundleDeploymentOptions{
			NamespaceLabels: map[string]string{"tier": "critical"},
		},
		fleet.BundleDeploymentOptions{
			Diff: diff,
		},
	)

	updateDeploymentFromStaged(tgt, &fleet.BundleStatus{}, &fleet.PartitionStatus{})

	a.Equal(diff, tgt.Deployment.Spec.Options.Diff)
	a.Nil(tgt.Deployment.Spec.Options.NamespaceLabels)
}

func TestUpdateDeploymentFromStaged_LeavesPausedTargetAlone(t *testing.T) {
	a := assert.New(t)

	tgt := newStagedTarget(
		fleet.BundleDeploymentOptions{
			NamespaceLabels: map[string]string{"tier": "critical"},
		},
		fleet.BundleDeploymentOptions{},
	)
	tgt.Bundle.Spec.Paused = true

	updateDeploymentFromStaged(tgt, &fleet.BundleStatus{}, &fleet.PartitionStatus{})

	a.Equal(map[string]string{"tier": "critical"}, tgt.Deployment.Spec.Options.NamespaceLabels)
}

// When the deployment is out of sync the whole staged options replace the
// current ones, namespace metadata included.
func TestUpdateDeploymentFromStaged_PromotesOutOfSyncDeployment(t *testing.T) {
	a := assert.New(t)

	tgt := newStagedTarget(
		fleet.BundleDeploymentOptions{
			NamespaceLabels: map[string]string{"tier": "critical"},
		},
		fleet.BundleDeploymentOptions{
			TargetNamespace: "elsewhere",
		},
	)
	tgt.Deployment.Spec.StagedDeploymentID = "manifest:other"

	updateDeploymentFromStaged(
		tgt,
		&fleet.BundleStatus{MaxUnavailable: 1},
		&fleet.PartitionStatus{MaxUnavailable: 1},
	)

	a.Equal("manifest:other", tgt.Deployment.Spec.DeploymentID)
	a.Equal("elsewhere", tgt.Deployment.Spec.Options.TargetNamespace)
	a.Nil(tgt.Deployment.Spec.Options.NamespaceLabels)
}

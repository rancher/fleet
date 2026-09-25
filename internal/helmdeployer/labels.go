package helmdeployer

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/name"
)

// maxLabelValueLength is the Kubernetes limit on the length of a label value.
const maxLabelValueLength = 63

// sourceIdentity is the provenance of a deployed resource: the Fleet resource
// that manages it. It is derived once per deploy from the labels of the
// BundleDeployment being reconciled and applied to every rendered object.
type sourceIdentity struct {
	Kind      string
	Namespace string
	Name      string
}

// yieldSourceOrigins determines which Fleet source a BundleDeployment
// originates from, based on the source labels the controller stamped onto it.
//
// It reports false when no source can be determined, either because the
// deployment carries no source label or because its bundle namespace is
// missing.
func yieldSourceOrigins(bdLabels map[string]string) (sourceIdentity, bool) {
	var s sourceIdentity

	if bdLabels[fleet.InternalBundleLabel] == "true" {
		return s, false
	}
	// GitRepo takes precedence over HelmOp: a BundleDeployment carrying both
	// labels is not expected, and preferring the GitRepo keeps the outcome
	// deterministic rather than dependent on map iteration.
	switch {
	case bdLabels[fleet.RepoLabel] != "":
		s.Kind = fleet.ManagedByKindGitRepo
		s.Name = bdLabels[fleet.RepoLabel]
	case bdLabels[fleet.HelmOpLabel] != "":
		s.Kind = fleet.ManagedByKindHelmOp
		s.Name = bdLabels[fleet.HelmOpLabel]
	case bdLabels[fleet.BundleLabel] != "":
		s.Kind = fleet.ManagedByKindBundle
		s.Name = bdLabels[fleet.BundleLabel]
	default:
		return sourceIdentity{}, false
	}

	s.Namespace = bdLabels[fleet.BundleNamespaceLabel]
	if s.Namespace == "" {
		return sourceIdentity{}, false
	}

	return s, true
}

// labels returns the labels to put onto every resource deployed
// from this source. A Name longer than the Kubernetes label value limit is
// shortened deterministically, so that repeated reconciles of an unchanged
// source produce identical values and two distinct names do not collide.
func (s sourceIdentity) labels() map[string]string {
	sourceName := s.Name
	if len(sourceName) > maxLabelValueLength {
		sourceName = name.SafeConcatName(sourceName)
	}

	return map[string]string{
		fleet.ManagedByKindLabel:      s.Kind,
		fleet.ManagedByNamespaceLabel: s.Namespace,
		fleet.ManagedByNameLabel:      sourceName,
	}
}

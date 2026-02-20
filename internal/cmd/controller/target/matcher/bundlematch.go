package matcher

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// BundleMatch stores the bundle and the matcher for the bundle
type BundleMatch struct {
	bundle  *fleet.Bundle
	matcher *matcher
}

type findCriteriaMatch func(targetMatch targetMatch, clusterName, clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool

func New(bundle *fleet.Bundle) (*BundleMatch, error) {
	bm := &BundleMatch{
		bundle: bundle,
	}
	return bm, bm.initMatcher()
}

func (a *BundleMatch) MatchForTarget(name string) *fleet.BundleTarget {
	for i, target := range a.bundle.Spec.Targets {
		if target.Name != name {
			continue
		}
		return &a.bundle.Spec.Targets[i]
	}
	return nil
}

// Match returns the first BundleTarget that matches the target criteria. Targets are evaluated in order.
// It checks for restrictions, which means that just targets included in the GitRepo can be returned. TargetCustomizations
// described in the fleet.yaml will be ignored.
// All GitRepo targets are added as TargetRestrictions, which acts as a whitelist.
func (a *BundleMatch) Match(clusterName string, clusterGroups map[string]map[string]string, clusterLabels map[string]string) *fleet.BundleTarget {
	if m := a.matcher.match(clusterName, clusterLabels, clusterGroups, a.matcher.criteriaWithRestrictions); m != nil {
		return m
	}

	return nil
}

// MatchTargetCustomizations returns the first BundleTarget that matches the target criteria. Targets are evaluated in order.
// It doesn't check for restrictions, which means TargetCustomizations described in the fleet.yaml are considered.
func (a *BundleMatch) MatchTargetCustomizations(clusterName string, clusterGroups map[string]map[string]string, clusterLabels map[string]string) *fleet.BundleTarget {
	if m := a.matcher.match(clusterName, clusterLabels, clusterGroups, criteriaWithoutRestrictions); m != nil {
		return m
	}

	return nil
}

// HasDoNotDeployTarget returns true if any target customization matching the given cluster
// has DoNotDeploy set to true. Unlike MatchTargetCustomizations, this scans all matching
// targets instead of stopping at the first match, so a doNotDeploy entry does not have to
// appear before a broader matching entry in the target list.
func (a *BundleMatch) HasDoNotDeployTarget(clusterName string, clusterGroups map[string]map[string]string, clusterLabels map[string]string) bool {
	for _, tm := range a.matcher.matches {
		if !tm.bundleTarget.DoNotDeploy {
			continue
		}
		if len(clusterGroups) == 0 {
			if criteriaWithoutRestrictions(tm, clusterName, "", nil, clusterLabels) {
				return true
			}
		} else {
			for cg, cgLabels := range clusterGroups {
				if criteriaWithoutRestrictions(tm, clusterName, cg, cgLabels, clusterLabels) {
					return true
				}
			}
		}
	}
	return false
}

type targetMatch struct {
	bundleTarget *fleet.BundleTarget
	criteria     *ClusterMatcher
}

type matcher struct {
	matches      []targetMatch
	restrictions []*ClusterMatcher
}

func (a *BundleMatch) initMatcher() error {
	m := &matcher{}

	for i, target := range a.bundle.Spec.Targets {
		clusterMatcher, err := NewClusterMatcher(target.ClusterName, target.ClusterGroup, target.ClusterGroupSelector, target.ClusterSelector)
		if err != nil {
			return err
		}
		t := targetMatch{
			bundleTarget: &a.bundle.Spec.Targets[i],
			criteria:     clusterMatcher,
		}

		m.matches = append(m.matches, t)
	}

	for _, target := range a.bundle.Spec.TargetRestrictions {
		clusterMatcher, err := NewClusterMatcher(target.ClusterName, target.ClusterGroup, target.ClusterGroupSelector, target.ClusterSelector)
		if err != nil {
			return err
		}
		m.restrictions = append(m.restrictions, clusterMatcher)
	}

	a.matcher = m
	return nil
}

func (m *matcher) isRestricted(clusterName, clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
	// There are no restrictions. That means this Bundle was not created by a GitRepo, and there are no targetCustomizations
	if len(m.restrictions) == 0 {
		return false
	}

	for _, restriction := range m.restrictions {
		if restriction.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
			return false
		}
	}

	// This target is a targetCustomization from a fleet.yaml
	return true
}

// checks if criteria is matched just if the target is inside the targetRestrictions. This is used for Targets defined
// in the GitRepo, since these targets are also added as targetRestrictions.
func (m *matcher) criteriaWithRestrictions(targetMatch targetMatch, clusterName, clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
	if !m.isRestricted(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) &&
		targetMatch.criteria.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
		return true
	}

	return false
}

// Checks targetMatch's criteria for a match on the specified cluster name, group and labels, without checking if target is inside the targetRestrictions. This is used for TargetCustomizations.
func criteriaWithoutRestrictions(targetMatch targetMatch, clusterName, clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
	return targetMatch.criteria.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels)
}

// match returns the first BundleTarget, from the matcher's target matches, which matches the specified cluster name, groups and labels, using matching logic implemented via findCriteriaMatch.
func (m *matcher) match(clusterName string, clusterLabels map[string]string, clusterGroups map[string]map[string]string, findCriteriaMatch findCriteriaMatch) *fleet.BundleTarget {
	for _, targetMatch := range m.matches {
		if len(clusterGroups) == 0 {
			if findCriteriaMatch(targetMatch, clusterName, "", nil, clusterLabels) {
				return targetMatch.bundleTarget
			}
		} else {
			for clusterGroup, clusterGroupLabels := range clusterGroups {
				if findCriteriaMatch(targetMatch, clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
					return targetMatch.bundleTarget
				}
			}
		}
	}

	return nil
}

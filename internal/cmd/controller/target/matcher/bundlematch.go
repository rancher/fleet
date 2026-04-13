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
// It checks for restrictions, which means that just targets included in the GitRepo can be returned.
// TargetCustomizations described in the fleet.yaml will be ignored.
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
	if m := a.matcher.matchCustomization(clusterName, clusterLabels, clusterGroups); m != nil {
		return m
	}

	return nil
}

// MatchAllTargetCustomizations returns all BundleTargets marked as customizations that match the target criteria, in list order.
// Used when TargetCustomizationMode is AllMatches.
func (a *BundleMatch) MatchAllTargetCustomizations(clusterName string, clusterGroups map[string]map[string]string, clusterLabels map[string]string) []*fleet.BundleTarget {
	var result []*fleet.BundleTarget
	for _, tm := range a.matcher.matches {
		if !tm.isCustomization {
			continue
		}
		if len(clusterGroups) == 0 {
			if tm.criteria.Match(clusterName, "", nil, clusterLabels) {
				result = append(result, tm.bundleTarget)
			}
		} else {
			for cg, cgLabels := range clusterGroups {
				if tm.criteria.Match(clusterName, cg, cgLabels, clusterLabels) {
					result = append(result, tm.bundleTarget)
					break // matched via one group — don't add the same target multiple times
				}
			}
		}
	}
	return result
}

type targetMatch struct {
	bundleTarget    *fleet.BundleTarget
	criteria        *ClusterMatcher
	isCustomization bool // true when this target comes from fleet.yaml targetCustomizations, not a GitRepo target
}

type matcher struct {
	matches      []targetMatch
	restrictions []*ClusterMatcher
}

func (a *BundleMatch) initMatcher() error {
	m := &matcher{}

	// Calculate position-based split point for fallback
	// The first N targets are customizations, where N = total - restrictions
	numRestrictions := len(a.bundle.Spec.TargetRestrictions)
	numCustomizations := len(a.bundle.Spec.Targets) - numRestrictions

	for i, target := range a.bundle.Spec.Targets {
		clusterMatcher, err := NewClusterMatcher(target.ClusterName, target.ClusterGroup, target.ClusterGroupSelector, target.ClusterSelector)
		if err != nil {
			return err
		}
		t := targetMatch{
			bundleTarget:    &a.bundle.Spec.Targets[i],
			criteria:        clusterMatcher,
			isCustomization: determineIsCustomization(target, i, numCustomizations, numRestrictions),
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

// determineIsCustomization uses explicit Source field if present,
// falls back to position-based detection for backward compatibility.
func determineIsCustomization(target fleet.BundleTarget, index int, numCustomizations int, numRestrictions int) bool {
	// NEW BUNDLES: Source field is populated by bundlereader
	// This is the preferred method for long-term maintainability
	if target.Source != "" {
		return target.Source == "customization"
	}

	// OLD BUNDLES: Source field is empty (created before this field existed)
	// Use position-based detection as fallback:
	// - bundlereader appends targetCustomizations first (read.go:187)
	// - Then appends GitRepo targets from targets file (read.go:332)
	// - Therefore: first N targets are customizations
	//   where N = len(Targets) - len(TargetRestrictions)
	//
	// SPECIAL CASE: If there are no targetRestrictions, this bundle wasn't
	// created by a GitRepo (e.g., CLI-loaded bundles, standalone bundles).
	// In this case, treat all targets as regular bundle targets (not customizations)
	// to maintain backward compatibility with bundles that predate the Source field.
	//
	// This fixes the collision bug for old Bundles without requiring recreation
	if numRestrictions == 0 {
		return false
	}
	return index < numCustomizations
}

func (m *matcher) isRestricted(clusterName, clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
	// There are no restrictions. That means this Bundle was not created by a GitRepo.
	if len(m.restrictions) == 0 {
		return false
	}

	for _, restriction := range m.restrictions {
		if restriction.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
			return false
		}
	}

	return true
}

// criteriaWithRestrictions checks that the cluster passes the restriction allowlist
// and matches the target's cluster selector. Used for GitRepo targets only;
// customization targets (from fleet.yaml) are excluded.
func (m *matcher) criteriaWithRestrictions(targetMatch targetMatch, clusterName, clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
	if targetMatch.isCustomization {
		return false
	}
	if !m.isRestricted(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) &&
		targetMatch.criteria.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
		return true
	}

	return false
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

// matchCustomization returns the first customization target that matches the cluster.
func (m *matcher) matchCustomization(clusterName string, clusterLabels map[string]string, clusterGroups map[string]map[string]string) *fleet.BundleTarget {
	for _, tm := range m.matches {
		if !tm.isCustomization {
			continue
		}
		if len(clusterGroups) == 0 {
			if tm.criteria.Match(clusterName, "", nil, clusterLabels) {
				return tm.bundleTarget
			}
		} else {
			for clusterGroup, clusterGroupLabels := range clusterGroups {
				if tm.criteria.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
					return tm.bundleTarget
				}
			}
		}
	}
	return nil
}

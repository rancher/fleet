package bundlematcher

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/match"
)

// BundleMatch stores the bundle and the matcher for the bundle
type BundleMatch struct {
	bundle  *fleet.Bundle
	matcher *matcher
}

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

func (a *BundleMatch) Match(clusterName string, clusterGroups map[string]map[string]string, clusterLabels map[string]string) *fleet.BundleTarget {
	for clusterGroup, clusterGroupLabels := range clusterGroups {
		if m := a.matcher.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels); m != nil {
			return m
		}
	}
	if len(clusterGroups) == 0 {
		return a.matcher.Match(clusterName, "", nil, clusterLabels)
	}
	return nil
}

type targetMatch struct {
	bundleTarget *fleet.BundleTarget
	criteria     *match.ClusterMatcher
}

type matcher struct {
	matches      []targetMatch
	restrictions []*match.ClusterMatcher
}

func (a *BundleMatch) initMatcher() error {
	var (
		m = &matcher{}
	)

	for i, target := range a.bundle.Spec.Targets {
		clusterMatcher, err := match.NewClusterMatcher(target.ClusterName, target.ClusterGroup, target.ClusterGroupSelector, target.ClusterSelector)
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
		clusterMatcher, err := match.NewClusterMatcher(target.ClusterName, target.ClusterGroup, target.ClusterGroupSelector, target.ClusterSelector)
		if err != nil {
			return err
		}
		m.restrictions = append(m.restrictions, clusterMatcher)
	}

	a.matcher = m
	return nil
}

func (m *matcher) isRestricted(clusterName, clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
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

func (m *matcher) Match(clusterName, clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) *fleet.BundleTarget {
	if m.isRestricted(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
		return nil
	}

	for _, targetMatch := range m.matches {
		if targetMatch.criteria.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
			return targetMatch.bundleTarget
		}
	}

	return nil
}

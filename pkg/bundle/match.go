package bundle

import (
	"github.com/rancher/fleet/pkg/match"
	"github.com/rancher/fleet/pkg/render"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	manifest "github.com/rancher/fleet/pkg/manifest"
)

type Match struct {
	Target   *fleet.BundleTarget
	Bundle   *Bundle
	manifest *manifest.Manifest
}

func (t *Match) Manifest() (*manifest.Manifest, error) {
	if t.manifest != nil {
		return t.manifest, nil
	}

	manifest, err := manifest.New(&t.Bundle.Definition.Spec, t.Target.Overlays...)
	if err != nil {
		return nil, err
	}

	// sanity test that patches are same
	if err := render.IsValid(t.Bundle.Definition.Name, manifest); err != nil {
		return nil, err
	}

	t.manifest = manifest
	return manifest, nil
}

func (a *Bundle) MatchForTarget(name string) *Match {
	for i, target := range a.Definition.Spec.Targets {
		if target.Name != name {
			continue
		}
		return &Match{
			Target: &a.Definition.Spec.Targets[i],
			Bundle: a,
		}
	}
	return nil
}

func (a *Bundle) Match(clusterGroups map[string]map[string]string, clusterLabels map[string]string) *Match {
	for clusterGroup, clusterGroupLabels := range clusterGroups {
		if m := a.matcher.Match(clusterGroup, clusterGroupLabels, clusterLabels); m != nil {
			return m
		}
	}
	if len(clusterGroups) == 0 {
		return a.matcher.Match("", nil, clusterLabels)
	}
	return nil
}

type targetMatch struct {
	targetBundle *Match
	criteria     *match.ClusterMatcher
}

type matcher struct {
	matches      []targetMatch
	restrictions []*match.ClusterMatcher
}

func (a *Bundle) initMatcher() error {
	var (
		m = &matcher{}
	)

	for i, target := range a.Definition.Spec.Targets {
		clusterMatcher, err := match.NewClusterMatcher(target.ClusterGroup, target.ClusterGroupSelector, target.ClusterSelector)
		if err != nil {
			return err
		}
		t := targetMatch{
			targetBundle: &Match{
				Target: &a.Definition.Spec.Targets[i],
				Bundle: a,
			},
			criteria: clusterMatcher,
		}

		m.matches = append(m.matches, t)
	}

	for _, target := range a.Definition.Spec.TargetRestrictions {
		clusterMatcher, err := match.NewClusterMatcher(target.ClusterGroup, target.ClusterGroupSelector, target.ClusterSelector)
		if err != nil {
			return err
		}
		m.restrictions = append(m.restrictions, clusterMatcher)
	}

	a.matcher = m
	return nil
}

func (m *matcher) isRestricted(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
	if len(m.restrictions) == 0 {
		return false
	}

	for _, restriction := range m.restrictions {
		if restriction.Match(clusterGroup, clusterGroupLabels, clusterLabels) {
			return false
		}
	}

	return true
}

func (m *matcher) Match(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) *Match {
	if m.isRestricted(clusterGroup, clusterGroupLabels, clusterLabels) {
		return nil
	}

	for _, targetMatch := range m.matches {
		if targetMatch.criteria.Match(clusterGroup, clusterGroupLabels, clusterLabels) {
			return targetMatch.targetBundle
		}
	}

	return nil
}

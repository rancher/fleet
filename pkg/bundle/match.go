package bundle

import (
	"github.com/rancher/fleet/pkg/render"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	manifest "github.com/rancher/fleet/pkg/manifest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

func (a *Bundle) Match(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) *Match {
	return a.matcher.Match(clusterGroup, clusterGroupLabels, clusterLabels)
}

type criteria func(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool

type targetMatch struct {
	targetBundle *Match
	criteria     []criteria
}

type matcher struct {
	matches []targetMatch
}

func toSelector(labels *metav1.LabelSelector) (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(labels)
}

func (a *Bundle) initMatcher() error {
	var (
		m = &matcher{}
	)

	for i, target := range a.Definition.Spec.Targets {
		t := targetMatch{
			targetBundle: &Match{
				Target: &a.Definition.Spec.Targets[i],
				Bundle: a,
			},
		}

		if target.ClusterGroup != "" {
			t.criteria = append(t.criteria, func(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
				return clusterGroup == target.ClusterGroup
			})
		}

		if target.ClusterGroupSelector != nil {
			selector, err := toSelector(target.ClusterGroupSelector)
			if err != nil {
				return err
			}
			t.criteria = append(t.criteria, func(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
				return selector.Matches(labels.Set(clusterGroupLabels))
			})
		}

		if target.ClusterSelector != nil {
			selector, err := toSelector(target.ClusterSelector)
			if err != nil {
				return err
			}
			t.criteria = append(t.criteria, func(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
				return selector.Matches(labels.Set(clusterLabels))
			})
		}

		m.matches = append(m.matches, t)
	}

	a.matcher = m
	return nil
}

func (m *matcher) Match(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) *Match {
outer:
	for _, targetMatch := range m.matches {
		if len(targetMatch.criteria) == 0 {
			continue
		}
		for _, criteria := range targetMatch.criteria {
			if !criteria(clusterGroup, clusterGroupLabels, clusterLabels) {
				continue outer
			}
		}
		return targetMatch.targetBundle
	}

	return nil
}

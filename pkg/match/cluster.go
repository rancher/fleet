package match

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type criteria func(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool

type ClusterMatcher struct {
	criteria []criteria
}

func toSelector(labels *metav1.LabelSelector) (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(labels)
}

func NewClusterMatcher(clusterGroup string, clusterGroupSelector *metav1.LabelSelector, clusterSelector *metav1.LabelSelector) (*ClusterMatcher, error) {
	t := &ClusterMatcher{}

	if clusterGroup != "" {
		t.criteria = append(t.criteria, func(clusterGroupTest string, _, _ map[string]string) bool {
			return clusterGroup == clusterGroupTest
		})
	}

	if clusterGroupSelector != nil {
		selector, err := toSelector(clusterGroupSelector)
		if err != nil {
			return nil, err
		}
		t.criteria = append(t.criteria, func(_ string, clusterGroupLabels, _ map[string]string) bool {
			return selector.Matches(labels.Set(clusterGroupLabels))
		})
	}

	if clusterSelector != nil {
		selector, err := toSelector(clusterSelector)
		if err != nil {
			return nil, err
		}
		t.criteria = append(t.criteria, func(_ string, _, clusterLabels map[string]string) bool {
			return selector.Matches(labels.Set(clusterLabels))
		})
	}

	return t, nil
}

func (t *ClusterMatcher) Match(clusterGroup string, clusterGroupLabels, clusterLabels map[string]string) bool {
	if len(t.criteria) == 0 {
		return false
	}
	for _, criteria := range t.criteria {
		if !criteria(clusterGroup, clusterGroupLabels, clusterLabels) {
			return false
		}
	}
	return true
}

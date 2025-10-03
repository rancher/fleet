package matcher

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// ScheduleMatch stores the schedule and the matcher for the schedule
type ScheduleMatch struct {
	schedule        *fleet.Schedule
	clusterMatchers []ClusterMatcher
}

// NewScheduleMatch returns a new ScheduleMatch initialized
func NewScheduleMatch(schedule *fleet.Schedule) (*ScheduleMatch, error) {
	bm := &ScheduleMatch{
		schedule: schedule,
	}

	return bm, bm.initMatcher()
}

// MatchCluster returns true if the given cluster name, cluster groups or cluster labels
// match any of the schedule matchers.
func (m *ScheduleMatch) MatchCluster(clusterName string, clusterGroups map[string]map[string]string, clusterLabels map[string]string) bool {
	for _, m := range m.clusterMatchers {
		if len(clusterGroups) == 0 {
			if m.Match(clusterName, "", nil, clusterLabels) {
				return true
			}
		} else {
			for clusterGroup, clusterGroupLabels := range clusterGroups {
				if m.Match(clusterName, clusterGroup, clusterGroupLabels, clusterLabels) {
					return true
				}
			}
		}
	}

	return false
}

func (m *ScheduleMatch) initMatcher() error {
	for _, target := range m.schedule.Spec.Targets.Clusters {
		clusterMatcher, err := NewClusterMatcher(
			target.ClusterName,
			target.ClusterGroup,
			target.ClusterGroupSelector,
			target.ClusterSelector,
		)
		if err != nil {
			return err
		}
		m.clusterMatchers = append(m.clusterMatchers, *clusterMatcher)
	}

	return nil
}

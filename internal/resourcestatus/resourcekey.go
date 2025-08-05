package resourcestatus

import (
	"sort"
	"strings"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func SetResources(list []fleet.BundleDeployment, status *fleet.StatusBase) {
	byCluster := fromResources(list)
	status.Resources = aggregateResourceStatesClustersMap(byCluster)
	status.ResourceCounts = sumResourceCounts(list)
	status.PerClusterResourceCounts = resourceCountsPerCluster(list)
}

func SetClusterResources(list []fleet.BundleDeployment, cluster *fleet.Cluster) {
	cluster.Status.ResourceCounts = sumResourceCounts(list)
}

func key(resource fleet.Resource) string {
	return resource.Type + "/" + resource.ID
}

func resourceCountsPerCluster(items []fleet.BundleDeployment) map[string]*fleet.ResourceCounts {
	res := make(map[string]*fleet.ResourceCounts)
	for _, bd := range items {
		clusterID := bd.Labels[fleet.ClusterNamespaceLabel] + "/" + bd.Labels[fleet.ClusterLabel]
		if _, ok := res[clusterID]; !ok {
			res[clusterID] = &fleet.ResourceCounts{}
		}
		summary.IncrementResourceCounts(res[clusterID], bd.Status.ResourceCounts)
	}
	return res
}

type resourceStateEntry struct {
	state      string
	clusterID  string
	incomplete bool
}

type resourceStatesByResourceKey map[fleet.ResourceKey][]resourceStateEntry

func clusterID(bd fleet.BundleDeployment) string {
	return bd.Labels[fleet.ClusterNamespaceLabel] + "/" + bd.Labels[fleet.ClusterLabel]
}

// fromResources inspects a list of BundleDeployments and returns a list of per-cluster states per resource keys.
// It also returns a list of errors messages produced that may have occurred during processing
func fromResources(items []fleet.BundleDeployment) resourceStatesByResourceKey {
	sort.Slice(items, func(i, j int) bool {
		return clusterID(items[i]) < clusterID(items[j])
	})

	resources := make(resourceStatesByResourceKey)
	for _, bd := range items {
		for key, entry := range bundleDeploymentResources(bd) {
			resources[key] = append(resources[key], entry)
		}
	}

	return resources
}

func resourceId(namespace, name string) string {
	if namespace != "" {
		return namespace + "/" + name
	}
	return name
}

func toType(apiVersion, kind string) string {
	group := strings.Split(apiVersion, "/")[0]
	if group == "v1" {
		group = ""
	} else if len(group) > 0 {
		group += "."
	}
	return group + strings.ToLower(kind)
}

// resourceDefaultState calculates the state for items in the status.Resources list.
// This default state may be replaced individually for each resource with the information from NonReadyStatus and ModifiedStatus fields.
func resourcesDefaultState(bd *fleet.BundleDeployment) string {
	switch bdState := summary.GetDeploymentState(bd); bdState {
	// NotReady and Modified BD states are inferred from resource statuses, so it's incorrect to use that to calculate resource states
	case fleet.NotReady, fleet.Modified:
		if bd.Status.IncompleteState {
			return "Unknown"
		} else {
			return string(fleet.Ready)
		}
	default:
		return string(bdState)
	}
}

func bundleDeploymentResources(bd fleet.BundleDeployment) map[fleet.ResourceKey]resourceStateEntry {
	clusterID := bd.Labels[fleet.ClusterNamespaceLabel] + "/" + bd.Labels[fleet.ClusterLabel]
	incomplete := bd.Status.IncompleteState
	defaultState := resourcesDefaultState(&bd)

	resources := make(map[fleet.ResourceKey]resourceStateEntry, len(bd.Status.Resources))
	for _, bdResource := range bd.Status.Resources {
		resourceKey := fleet.ResourceKey{
			Kind:       bdResource.Kind,
			APIVersion: bdResource.APIVersion,
			Name:       bdResource.Name,
			Namespace:  bdResource.Namespace,
		}
		resources[resourceKey] = resourceStateEntry{
			state:      defaultState,
			clusterID:  clusterID,
			incomplete: incomplete,
		}
	}

	for _, nonReady := range bd.Status.NonReadyStatus {
		resourceKey := fleet.ResourceKey{
			Kind:       nonReady.Kind,
			APIVersion: nonReady.APIVersion,
			Namespace:  nonReady.Namespace,
			Name:       nonReady.Name,
		}
		resources[resourceKey] = resourceStateEntry{
			state:      nonReady.Summary.State,
			clusterID:  clusterID,
			incomplete: incomplete,
		}
	}

	for _, modified := range bd.Status.ModifiedStatus {
		key := fleet.ResourceKey{
			Kind:       modified.Kind,
			APIVersion: modified.APIVersion,
			Namespace:  modified.Namespace,
			Name:       modified.Name,
		}
		state := "Modified"
		if modified.Delete {
			state = "Orphaned"
		} else if modified.Create {
			state = "Missing"
		}
		resources[key] = resourceStateEntry{
			state:      state,
			clusterID:  clusterID,
			incomplete: incomplete,
		}
	}

	return resources
}

func aggregateResourceStatesClustersMap(resourceKeyStates resourceStatesByResourceKey) []fleet.Resource {
	result := make([]fleet.Resource, 0, len(resourceKeyStates))
	for resourceKey, entries := range resourceKeyStates {
		resource := &fleet.Resource{
			Kind:       resourceKey.Kind,
			APIVersion: resourceKey.APIVersion,
			Namespace:  resourceKey.Namespace,
			Name:       resourceKey.Name,
			State:      "Ready",
			Type:       toType(resourceKey.APIVersion, resourceKey.Kind),
			ID:         resourceId(resourceKey.Namespace, resourceKey.Name),
		}

		for _, entry := range entries {
			if entry.incomplete {
				resource.IncompleteState = true
			}

			appendToPerClusterState(&resource.PerClusterState, entry.state, entry.clusterID)

			// top-level state is set from first non "Ready" per-cluster state
			if resource.State == "Ready" {
				resource.State = entry.state
			}
		}

		result = append(result, *resource)
	}

	sort.Slice(result, func(i, j int) bool {
		return key(result[i]) < key(result[j])
	})

	return result
}

func appendToPerClusterState(states *fleet.PerClusterState, state, clusterID string) {
	switch state {
	case "Ready":
		states.Ready = append(states.Ready, clusterID)
	case "WaitApplied":
		states.WaitApplied = append(states.WaitApplied, clusterID)
	case "Pending":
		states.Pending = append(states.Pending, clusterID)
	case "Modified":
		states.Modified = append(states.Modified, clusterID)
	case "NotReady", "updating":
		// `updating` comes from checkTransitioning summarizer in the
		// fleet-agent. When the `Available` condition is set to false, the
		// state is set to `updating`, but we treat it as nonReady for per
		// cluster states.
		states.NotReady = append(states.NotReady, clusterID)
	case "Orphaned":
		states.Orphaned = append(states.Orphaned, clusterID)
	case "Missing":
		states.Missing = append(states.Missing, clusterID)
	case "Unknown":
		states.Unknown = append(states.Unknown, clusterID)
	default:
		// ignore
	}
}

func sumResourceCounts(items []fleet.BundleDeployment) fleet.ResourceCounts {
	var res fleet.ResourceCounts
	for _, bd := range items {
		summary.IncrementResourceCounts(&res, bd.Status.ResourceCounts)
	}
	return res
}

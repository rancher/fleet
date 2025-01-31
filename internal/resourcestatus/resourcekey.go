package resourcestatus

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func SetResources(list []fleet.BundleDeployment, status *fleet.StatusBase) {
	byCluster, errors := fromResources(list)
	status.ResourceErrors = errors
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
	fleet.ResourcePerClusterState
	incomplete bool
}

type resourceStatesByResourceKey map[fleet.ResourceKey][]resourceStateEntry

func clusterID(bd fleet.BundleDeployment) string {
	return bd.Labels[fleet.ClusterNamespaceLabel] + "/" + bd.Labels[fleet.ClusterLabel]
}

// fromResources inspects a list of BundleDeployments and returns a list of per-cluster states per resource keys.
// It also returns a list of errors messages produced that may have occurred during processing
func fromResources(items []fleet.BundleDeployment) (resourceStatesByResourceKey, []string) {
	sort.Slice(items, func(i, j int) bool {
		return clusterID(items[i]) < clusterID(items[j])
	})
	var (
		errors    []string
		resources = make(resourceStatesByResourceKey)
	)
	for _, bd := range items {
		clusterID := bd.Labels[fleet.ClusterNamespaceLabel] + "/" + bd.Labels[fleet.ClusterLabel]

		bdResources, errs := bundleDeploymentResources(bd)
		if len(errs) > 0 {
			for _, err := range errs {
				errors = append(errors, err.Error())
			}
		}
		for key, state := range bdResources {
			state.ClusterID = clusterID
			resources[key] = append(resources[key], resourceStateEntry{state, bd.Status.IncompleteState})
		}
	}

	sort.Strings(errors)

	return resources, errors
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

func bundleDeploymentResources(bd fleet.BundleDeployment) (map[fleet.ResourceKey]fleet.ResourcePerClusterState, []error) {
	defaultState := string(summary.GetDeploymentState(&bd))

	resources := make(map[fleet.ResourceKey]fleet.ResourcePerClusterState, len(bd.Status.Resources))
	for _, bdResource := range bd.Status.Resources {
		resourceKey := fleet.ResourceKey{
			Kind:       bdResource.Kind,
			APIVersion: bdResource.APIVersion,
			Name:       bdResource.Name,
			Namespace:  bdResource.Namespace,
		}
		resources[resourceKey] = fleet.ResourcePerClusterState{
			State: defaultState,
		}
	}

	for _, nonReady := range bd.Status.NonReadyStatus {
		resourceKey := fleet.ResourceKey{
			Kind:       nonReady.Kind,
			APIVersion: nonReady.APIVersion,
			Namespace:  nonReady.Namespace,
			Name:       nonReady.Name,
		}
		resources[resourceKey] = fleet.ResourcePerClusterState{
			State:         nonReady.Summary.State,
			Error:         nonReady.Summary.Error,
			Transitioning: nonReady.Summary.Transitioning,
			Message:       strings.Join(nonReady.Summary.Message, "; "),
		}
	}

	var errors []error
	for _, modified := range bd.Status.ModifiedStatus {
		key := fleet.ResourceKey{
			Kind:       modified.Kind,
			APIVersion: modified.APIVersion,
			Namespace:  modified.Namespace,
			Name:       modified.Name,
		}
		state := fleet.ResourcePerClusterState{
			State: "Modified",
		}
		if modified.Delete {
			state.State = "Orphaned"
		} else if modified.Create {
			state.State = "Missing"
		} else if len(modified.Patch) > 0 {
			state.Patch = &fleet.GenericMap{}
			if err := json.Unmarshal([]byte(modified.Patch), state.Patch); err != nil {
				errors = append(errors, err)
			}
		}
		resources[key] = state
	}

	return resources, errors
}

func aggregateResourceStatesClustersMap(resourceKeyStates resourceStatesByResourceKey) []fleet.Resource {
	byResourceKey := make(map[fleet.ResourceKey]*fleet.Resource)
	for resourceKey, entries := range resourceKeyStates {
		if _, ok := byResourceKey[resourceKey]; !ok {
			byResourceKey[resourceKey] = &fleet.Resource{
				Kind:       resourceKey.Kind,
				APIVersion: resourceKey.APIVersion,
				Namespace:  resourceKey.Namespace,
				Name:       resourceKey.Name,
				State:      "Ready",
				Type:       toType(resourceKey.APIVersion, resourceKey.Kind),
				ID:         resourceId(resourceKey.Namespace, resourceKey.Name),
			}
		}
		resource := byResourceKey[resourceKey]

		for _, entry := range entries {
			if entry.incomplete {
				resource.IncompleteState = true
			}

			// "Ready" states are currently omitted
			if entry.State == "Ready" {
				continue
			}

			resource.PerClusterState = append(resource.PerClusterState, entry.ResourcePerClusterState)

			// top-level state is set from first non "Ready" per-cluster state
			if resource.State == "Ready" {
				resource.State = entry.State
			}
		}
	}

	result := make([]fleet.Resource, 0, len(byResourceKey))
	for _, resource := range byResourceKey {
		result = append(result, *resource)
	}
	sort.Slice(result, func(i, j int) bool {
		return key(result[i]) < key(result[j])
	})

	return result
}

func sumResourceCounts(items []fleet.BundleDeployment) fleet.ResourceCounts {
	var res fleet.ResourceCounts
	for _, bd := range items {
		summary.IncrementResourceCounts(&res, bd.Status.ResourceCounts)
	}
	return res
}

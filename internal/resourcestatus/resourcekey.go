package resourcestatus

import (
	"encoding/json"
	"sort"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func SetGitRepoResources(list *fleet.BundleDeploymentList, gitrepo *fleet.GitRepo) {
	s := summaryState(gitrepo.Status.Summary)
	r, errors := fromResources(list, s)
	gitrepo.Status.ResourceErrors = errors
	gitrepo.Status.ResourceCounts = countResources(r)
	gitrepo.Status.Resources = merge(r)
}

func SetClusterResources(list *fleet.BundleDeploymentList, cluster *fleet.Cluster) {
	s := summaryState(cluster.Status.Summary)
	r, _ := fromResources(list, s)
	cluster.Status.ResourceCounts = countResources(r)
}

// merge takes a list of GitRepo resources and deduplicates resources deployed to multiple clusters,
// ensuring that for such resources, the output contains a single resource entry with a field summarizing
// its status on each cluster.
func merge(resources []fleet.GitRepoResource) []fleet.GitRepoResource {
	merged := map[string]fleet.GitRepoResource{}
	for _, resource := range resources {
		key := key(resource)
		if existing, ok := merged[key]; ok {
			existing.PerClusterState = append(existing.PerClusterState, resource.PerClusterState...)
			merged[key] = existing
		} else {
			merged[key] = resource
		}
	}

	var result []fleet.GitRepoResource
	for _, resource := range merged {
		result = append(result, resource)
	}

	sort.Slice(result, func(i, j int) bool {
		return key(result[i]) < key(result[j])
	})
	return result
}

func key(resource fleet.GitRepoResource) string {
	return resource.Type + "/" + resource.ID
}

func summaryState(summary fleet.BundleSummary) string {
	if summary.WaitApplied > 0 {
		return "WaitApplied"
	}
	if summary.ErrApplied > 0 {
		return "ErrApplied"
	}
	return ""
}

// fromResources inspects all bundledeployments for this GitRepo and returns a list of
// GitRepoResources and error messages.
//
// It populates gitrepo status resources from bundleDeployments. BundleDeployment.Status.Resources is the list of deployed resources.
func fromResources(list *fleet.BundleDeploymentList, summaryState string) ([]fleet.GitRepoResource, []string) {
	var (
		resources []fleet.GitRepoResource
		errors    []string
	)

	for _, bd := range list.Items {
		bdResources := bundleDeploymentResources(bd)
		incomplete, err := addState(bd, bdResources)

		if len(err) > 0 {
			incomplete = true
			for _, err := range err {
				errors = append(errors, err.Error())
			}
		}

		for k, perCluster := range bdResources {
			resource := toResourceState(k, perCluster, incomplete, summaryState)
			resources = append(resources, resource)
		}
	}

	sort.Strings(errors)

	return resources, errors
}

func toResourceState(k fleet.ResourceKey, perCluster []fleet.ResourcePerClusterState, incomplete bool, summaryState string) fleet.GitRepoResource {
	resource := fleet.GitRepoResource{
		APIVersion:      k.APIVersion,
		Kind:            k.Kind,
		Namespace:       k.Namespace,
		Name:            k.Name,
		IncompleteState: incomplete,
		PerClusterState: perCluster,
	}
	resource.Type, resource.ID = toType(resource)

	for _, state := range perCluster {
		resource.State = state.State
		resource.Error = state.Error
		resource.Transitioning = state.Transitioning
		resource.Message = state.Message
		break
	}

	// fallback to state from gitrepo summary
	if resource.State == "" {
		if resource.IncompleteState {
			if summaryState != "" {
				resource.State = summaryState
			} else {
				resource.State = "Unknown"
			}
		} else if summaryState != "" {
			resource.State = summaryState
		} else {
			resource.State = "Ready"
		}
	}

	sort.Slice(perCluster, func(i, j int) bool {
		return perCluster[i].ClusterID < perCluster[j].ClusterID
	})
	return resource
}

func toType(resource fleet.GitRepoResource) (string, string) {
	group := strings.Split(resource.APIVersion, "/")[0]
	if group == "v1" {
		group = ""
	} else if len(group) > 0 {
		group += "."
	}
	t := group + strings.ToLower(resource.Kind)
	if resource.Namespace == "" {
		return t, resource.Name
	}
	return t, resource.Namespace + "/" + resource.Name
}

// addState adds per-cluster state information for nonReady and modified resources in a bundleDeployment.
// It only adds up to 10 entries to not overwhelm the status.
// It mutates resources and returns whether the reported state is incomplete and any errors encountered.
func addState(bd fleet.BundleDeployment, resources map[fleet.ResourceKey][]fleet.ResourcePerClusterState) (bool, []error) {
	var (
		incomplete bool
		errors     []error
	)

	if len(bd.Status.NonReadyStatus) >= 10 || len(bd.Status.ModifiedStatus) >= 10 {
		incomplete = true
	}

	cluster := bd.Labels[fleet.ClusterNamespaceLabel] + "/" + bd.Labels[fleet.ClusterLabel]
	for _, nonReady := range bd.Status.NonReadyStatus {
		key := fleet.ResourceKey{
			Kind:       nonReady.Kind,
			APIVersion: nonReady.APIVersion,
			Namespace:  nonReady.Namespace,
			Name:       nonReady.Name,
		}
		state := fleet.ResourcePerClusterState{
			State:         nonReady.Summary.State,
			Error:         nonReady.Summary.Error,
			Transitioning: nonReady.Summary.Transitioning,
			Message:       strings.Join(nonReady.Summary.Message, "; "),
			ClusterID:     cluster,
		}
		appendState(resources, key, state)
	}

	for _, modified := range bd.Status.ModifiedStatus {
		key := fleet.ResourceKey{
			Kind:       modified.Kind,
			APIVersion: modified.APIVersion,
			Namespace:  modified.Namespace,
			Name:       modified.Name,
		}
		state := fleet.ResourcePerClusterState{
			State:     "Modified",
			ClusterID: cluster,
		}
		if modified.Delete {
			state.State = "Orphaned"
		} else if modified.Create {
			state.State = "Missing"
		} else if len(modified.Patch) > 0 {
			state.Patch = &fleet.GenericMap{}
			err := json.Unmarshal([]byte(modified.Patch), state.Patch)
			if err != nil {
				errors = append(errors, err)
			}
		}
		appendState(resources, key, state)
	}
	return incomplete, errors
}

func appendState(states map[fleet.ResourceKey][]fleet.ResourcePerClusterState, key fleet.ResourceKey, state fleet.ResourcePerClusterState) {
	if existing, ok := states[key]; ok || key.Namespace != "" {
		states[key] = append(existing, state)
		return
	}

	for k, existing := range states {
		if k.Name == key.Name &&
			k.APIVersion == key.APIVersion &&
			k.Kind == key.Kind {
			delete(states, key)
			k.Namespace = ""
			states[k] = append(existing, state)
		}
	}
}

func bundleDeploymentResources(bd fleet.BundleDeployment) map[fleet.ResourceKey][]fleet.ResourcePerClusterState {
	bdResources := map[fleet.ResourceKey][]fleet.ResourcePerClusterState{}
	for _, resource := range bd.Status.Resources {
		key := fleet.ResourceKey{
			Kind:       resource.Kind,
			APIVersion: resource.APIVersion,
			Name:       resource.Name,
			Namespace:  resource.Namespace,
		}
		bdResources[key] = []fleet.ResourcePerClusterState{}
	}
	return bdResources
}

func countResources(resources []fleet.GitRepoResource) fleet.GitRepoResourceCounts {
	counts := fleet.GitRepoResourceCounts{}

	for _, resource := range resources {
		counts.DesiredReady++
		switch resource.State {
		case "Ready":
			counts.Ready++
		case "WaitApplied":
			counts.WaitApplied++
		case "Modified":
			counts.Modified++
		case "Orphan":
			counts.Orphaned++
		case "Missing":
			counts.Missing++
		case "Unknown":
			counts.Unknown++
		default:
			counts.NotReady++
		}
	}

	return counts
}

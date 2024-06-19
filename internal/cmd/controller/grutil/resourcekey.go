package grutil

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SetStatusFromResourceKey(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) {
	state := bundleErrorState(gitrepo.Status.Summary)
	gitrepo.Status.Resources, gitrepo.Status.ResourceErrors = fromResourceKey(ctx, c, gitrepo.Namespace, gitrepo.Name, state)
	gitrepo.Status = countResources(gitrepo.Status)
}

func bundleErrorState(summary fleet.BundleSummary) string {
	bundleErrorState := ""
	if summary.WaitApplied > 0 {
		bundleErrorState = "WaitApplied"
	}
	if summary.ErrApplied > 0 {
		bundleErrorState = "ErrApplied"
	}
	return bundleErrorState
}

// fromResourceKey lists all bundledeployments for this GitRepo and returns a list of
// GitRepoResource states for all resources
//
// It populates gitrepo status resources from bundleDeployments. BundleDeployment.Status.Resources is the list of deployed resources.
func fromResourceKey(ctx context.Context, c client.Client, namespace, name string, bundleErrorState string) ([]fleet.GitRepoResource, []string) {
	var (
		resources []fleet.GitRepoResource
		errors    []string
	)

	bdList := &fleet.BundleDeploymentList{}
	err := c.List(ctx, bdList, client.MatchingLabels{
		fleet.RepoLabel:            name,
		fleet.BundleNamespaceLabel: namespace,
	})
	if err != nil {
		errors = append(errors, err.Error())
		return resources, errors
	}

	for _, bd := range bdList.Items {
		bd := bd // fix gosec warning regarding "Implicit memory aliasing in for loop"
		bdResources := bundleDeploymentResources(bd)
		incomplete, err := addState(bd, bdResources)

		if len(err) > 0 {
			incomplete = true
			for _, err := range err {
				errors = append(errors, err.Error())
			}
		}

		for k, state := range bdResources {
			resource := toResourceState(k, state, incomplete, bundleErrorState)
			resources = append(resources, resource)
		}
	}

	sort.Strings(errors)
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Type+"/"+resources[i].ID < resources[j].Type+"/"+resources[j].ID
	})

	return resources, errors
}

func toResourceState(k fleet.ResourceKey, state []fleet.ResourcePerClusterState, incomplete bool, bundleErrorState string) fleet.GitRepoResource {
	resource := fleet.GitRepoResource{
		APIVersion:      k.APIVersion,
		Kind:            k.Kind,
		Namespace:       k.Namespace,
		Name:            k.Name,
		IncompleteState: incomplete,
		PerClusterState: state,
	}
	resource.Type, resource.ID = toType(resource)

	for _, state := range state {
		resource.State = state.State
		resource.Error = state.Error
		resource.Transitioning = state.Transitioning
		resource.Message = state.Message
		break
	}

	if resource.State == "" {
		if resource.IncompleteState {
			if bundleErrorState != "" {
				resource.State = bundleErrorState
			} else {
				resource.State = "Unknown"
			}
		} else if bundleErrorState != "" {
			resource.State = bundleErrorState
		} else {
			resource.State = "Ready"
		}
	}

	sort.Slice(state, func(i, j int) bool {
		return state[i].ClusterID < state[j].ClusterID
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

func addState(bd fleet.BundleDeployment, resources map[fleet.ResourceKey][]fleet.ResourcePerClusterState) (bool, []error) {
	var (
		incomplete bool
		errors     []error
	)

	if len(bd.Status.NonReadyStatus) >= 10 || len(bd.Status.ModifiedStatus) >= 10 {
		incomplete = true
	}

	cluster := bd.Labels[v1alpha1.ClusterNamespaceLabel] + "/" + bd.Labels[v1alpha1.ClusterLabel]
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
		resourceKey := fleet.ResourceKey{
			Kind:       resource.Kind,
			APIVersion: resource.APIVersion,
			Name:       resource.Name,
			Namespace:  resource.Namespace,
		}
		bdResources[resourceKey] = []fleet.ResourcePerClusterState{}
	}
	return bdResources
}

func countResources(status fleet.GitRepoStatus) fleet.GitRepoStatus {
	status.ResourceCounts = fleet.GitRepoResourceCounts{}

	for _, resource := range status.Resources {
		status.ResourceCounts.DesiredReady++
		switch resource.State {
		case "Ready":
			status.ResourceCounts.Ready++
		case "WaitApplied":
			status.ResourceCounts.WaitApplied++
		case "Modified":
			status.ResourceCounts.Modified++
		case "Orphan":
			status.ResourceCounts.Orphaned++
		case "Missing":
			status.ResourceCounts.Missing++
		case "Unknown":
			status.ResourceCounts.Unknown++
		default:
			status.ResourceCounts.NotReady++
		}
	}

	return status
}

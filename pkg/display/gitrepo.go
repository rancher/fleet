package display

import (
	"encoding/json"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sort"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
)

type Factory struct {
	bundleCache      fleetcontrollers.BundleCache
	bundles          fleetcontrollers.BundleController
	bundleDeployment fleetcontrollers.BundleDeploymentCache
}

func NewFactory(bundleCache fleetcontrollers.BundleCache, bundles fleetcontrollers.BundleController, bundleDeployment fleetcontrollers.BundleDeploymentCache) *Factory {
	return &Factory{
		bundleCache:      bundleCache,
		bundles:          bundles,
		bundleDeployment: bundleDeployment,
	}
}

func (b *Factory) Render(namespace, name string, bundleErrorState string) ([]fleet.GitRepoResource, []string) {
	var (
		resources []fleet.GitRepoResource
		errors    []string
	)

	//bundles, err := b.bundleCache.List(namespace, labels.SelectorFromSet(labels.Set{
	//	fleet.RepoLabel: name,
	//}))

	bundles, err := b.bundles.List(namespace, v1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{
			fleet.RepoLabel: name,
		}).String(),
	})
	if err != nil {
		errors = append(errors, err.Error())
		return resources, errors
	}

	for _, bundle := range bundles.Items {
		bundleDeployments, _ := GetBundleDeploymentsForBundle(b.bundleDeployment, &bundle)
		bundleResources := bundleResources(bundleDeployments)
		incomplete, err := addState(&bundle, bundleResources)
		if len(err) > 0 {
			incomplete = true
			for _, err := range err {
				errors = append(errors, err.Error())
			}
		}

		for k, state := range bundleResources {
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

func GetBundleDeploymentsForBundle(bundleDeployment fleetcontrollers.BundleDeploymentCache, app *fleet.Bundle) (result []*fleet.BundleDeployment, err error) {

	bundleDeployments, err := bundleDeployment.List("", labels.SelectorFromSet(DeploymentLabelsForSelector(app)))
	if err != nil {
		return nil, err
	}

	return bundleDeployments, nil
}

func DeploymentLabelsForSelector(app *fleet.Bundle) map[string]string {
	return map[string]string{
		"fleet.cattle.io/bundle-name":      app.Name,
		"fleet.cattle.io/bundle-namespace": app.Namespace,
	}
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

func addState(bundle *fleet.Bundle, resources map[fleet.ResourceKey][]fleet.ResourcePerClusterState) (bool, []error) {
	var (
		incomplete bool
		errors     []error
	)

	if len(bundle.Status.Summary.NonReadyResources) >= 10 {
		incomplete = true
	}

	for _, nonReadyResource := range bundle.Status.Summary.NonReadyResources {
		if len(nonReadyResource.NonReadyStatus) >= 10 || len(nonReadyResource.ModifiedStatus) >= 10 {
			incomplete = true
		}

		for _, nonReady := range nonReadyResource.NonReadyStatus {
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
				ClusterID:     nonReadyResource.Name,
			}
			appendState(resources, key, state)
		}

		for _, modified := range nonReadyResource.ModifiedStatus {
			key := fleet.ResourceKey{
				Kind:       modified.Kind,
				APIVersion: modified.APIVersion,
				Namespace:  modified.Namespace,
				Name:       modified.Name,
			}
			state := fleet.ResourcePerClusterState{
				State:     "Modified",
				ClusterID: nonReadyResource.Name,
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

func bundleResources(bundleDeployments []*fleet.BundleDeployment) map[fleet.ResourceKey][]fleet.ResourcePerClusterState {
	bundleResources := map[fleet.ResourceKey][]fleet.ResourcePerClusterState{}
	for _, bd := range bundleDeployments {
		for _, resourceKey := range bd.Status.ResourceKey {
			bundleResources[resourceKey] = []fleet.ResourcePerClusterState{}
		}
	}
	return bundleResources
}

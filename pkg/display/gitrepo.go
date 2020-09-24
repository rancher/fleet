package display

import (
	"encoding/json"
	"sort"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/options"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Factory struct {
	bundleCache fleetcontrollers.BundleCache
}

func NewFactory(bundleCache fleetcontrollers.BundleCache) *Factory {
	return &Factory{
		bundleCache: bundleCache,
	}
}

type key struct {
	Kind       string
	APIVersion string
	Namespace  string
	Name       string
}

func (b *Factory) Render(namespace, name string, waitApplied bool, isNSed func(schema.GroupVersionKind) bool) ([]fleet.GitRepoResource, []string) {
	var (
		resources []fleet.GitRepoResource
		errors    []string
	)

	bundles, err := b.bundleCache.List(namespace, labels.SelectorFromSet(labels.Set{
		fleet.RepoLabel: name,
	}))
	if err != nil {
		errors = append(errors, err.Error())
		return resources, errors
	}

	for _, bundle := range bundles {
		bundleResources, err := bundleResources(bundle, isNSed)
		if len(err) > 0 {
			for _, err := range err {
				errors = append(errors, err.Error())
			}
			continue
		}

		incomplete, err := addState(bundle, bundleResources)
		if len(err) > 0 {
			incomplete = true
			for _, err := range err {
				errors = append(errors, err.Error())
			}
		}

		for k, state := range bundleResources {
			resource := toResourceState(k, state, incomplete, waitApplied)
			resources = append(resources, resource)
		}
	}

	sort.Strings(errors)
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Type+"/"+resources[i].ID < resources[j].Type+"/"+resources[j].ID
	})

	return resources, errors
}

func toResourceState(k key, state []fleet.ResourcePerClusterState, incomplete, waitApplied bool) fleet.GitRepoResource {
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
			if waitApplied {
				resource.State = "WaitApplied"
			} else {
				resource.State = "Unknown"
			}
		} else if waitApplied {
			resource.State = "WaitApplied"
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

func addState(bundle *fleet.Bundle, resources map[key][]fleet.ResourcePerClusterState) (bool, []error) {
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
			key := key{
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
			key := key{
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

func appendState(states map[key][]fleet.ResourcePerClusterState, key key, state fleet.ResourcePerClusterState) {
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

func bundleResources(bundle *fleet.Bundle, isNSed func(schema.GroupVersionKind) bool) (map[key][]fleet.ResourcePerClusterState, []error) {
	var (
		errors          []error
		bundleResources = map[key][]fleet.ResourcePerClusterState{}
	)

	m, err := manifest.New(&bundle.Spec)
	if err != nil {
		errors = append(errors, err)
		return nil, errors
	}

	for _, target := range bundle.Spec.Targets {
		opts := options.Calculate(&bundle.Spec, &target)
		objs, err := helmdeployer.Template(bundle.Name, m, opts)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		for _, obj := range objs {
			m, err := meta.Accessor(obj)
			if err != nil {
				errors = append(errors, err)
				continue
			}
			key := key{
				Namespace: m.GetNamespace(),
				Name:      m.GetName(),
			}
			gvk := obj.GetObjectKind().GroupVersionKind()
			if key.Namespace == "" && isNSed(gvk) {
				if opts.DefaultNamespace == "" {
					key.Namespace = "default"
				} else {
					key.Namespace = opts.DefaultNamespace
				}
			}
			key.APIVersion, key.Kind = gvk.ToAPIVersionAndKind()
			bundleResources[key] = []fleet.ResourcePerClusterState{}
		}
	}

	return bundleResources, errors
}

package update

import (
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// ImageRef represents the image reference used to replace a field
// value in an update.
type ImageRef interface {
	// String returns a string representation of the image ref as it
	// is used in the update; e.g., "helloworld:v1.0.1"
	String() string
	// Identifier returns the tag or digest; e.g., "v1.0.1"
	Identifier() string
	// Repository returns the repository component of the ImageRef,
	// with an implied defaults, e.g., "library/helloworld"
	Repository() string
	// Registry returns the registry component of the ImageRef, e.g.,
	// "index.docker.io"
	Registry() string
	// Name gives the fully-qualified reference name, e.g.,
	// "index.docker.io/library/helloworld:v1.0.1"
	Name() string
	// Policy gives the namespaced name of the image policy that led
	// to the update.
	Policy() types.NamespacedName
}

type imageRef struct {
	name.Reference
	policy types.NamespacedName
}

// Policy gives the namespaced name of the policy that led to the
// update.
func (i imageRef) Policy() types.NamespacedName {
	return i.policy
}

// Repository gives the repository component of the image ref.
func (i imageRef) Repository() string {
	return i.Context().RepositoryStr()
}

// Registry gives the registry component of the image ref.
func (i imageRef) Registry() string {
	return i.Context().Registry.String()
}

// ObjectIdentifier holds the identifying data for a particular
// object. This won't always have a name (e.g., a kustomization.yaml).
type ObjectIdentifier struct {
	yaml.ResourceIdentifier
}

// Result reports the outcome of an automated update. It has a nested
// structure file->objects->images. Different projections (e.g., all
// the images, regardless of object) are available via methods.
type Result struct {
	Files map[string]FileResult
}

// FileResult gives the updates in a particular file.
type FileResult struct {
	Objects map[ObjectIdentifier][]ImageRef
}

// Images returns all the images that were involved in at least one
// update.
func (r Result) Images() []ImageRef {
	seen := make(map[ImageRef]struct{})
	var result []ImageRef
	for _, file := range r.Files {
		for _, images := range file.Objects {
			for _, ref := range images {
				if _, ok := seen[ref]; !ok {
					seen[ref] = struct{}{}
					result = append(result, ref)
				}
			}
		}
	}
	return result
}

// Objects returns a map of all the objects against the images updated
// within, regardless of which file they appear in.
func (r Result) Objects() map[ObjectIdentifier][]ImageRef {
	result := make(map[ObjectIdentifier][]ImageRef)
	for _, file := range r.Files {
		for res, refs := range file.Objects {
			result[res] = append(result[res], refs...)
		}
	}
	return result
}

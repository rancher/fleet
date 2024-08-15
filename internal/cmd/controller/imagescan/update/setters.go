package update

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"sigs.k8s.io/kustomize/kyaml/fieldmeta"
	"sigs.k8s.io/kustomize/kyaml/kio"
	"sigs.k8s.io/kustomize/kyaml/kio/kioutil"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

const (
	// This is preserved from setters2
	K8sCliExtensionKey = "x-k8s-cli"
)

// Credit: https://github.com/fluxcd/image-automation-controller

const (
	// SetterShortHand is a shorthand that can be used to mark
	// setters; instead of
	// # { "$ref": "#/definitions/
	SetterShortHand = "$imagescan"
)

func init() {
	fieldmeta.SetShortHandRef(SetterShortHand)
}

// WithSetters takes all YAML files from `inpath`, updates any
// that contain an "in scope" image policy marker, and writes files it
// updated (and only those files) back to `outpath`.
func WithSetters(inpath, outpath string, scans []*v1alpha1.ImageScan) error {
	var settersSchema spec.Schema

	// collect setter defs and setters by going through all the image
	// policies available.
	result := Result{
		Files: make(map[string]FileResult),
	}
	// the OpenAPI schema is a package variable in kyaml/openapi. In
	// lieu of being able to isolate invocations (per
	// https://github.com/kubernetes-sigs/kustomize/issues/3058), I
	// serialise access to it and reset it each time.

	// construct definitions

	// the format of the definitions expected is given here:
	//     https://github.com/kubernetes-sigs/kustomize/blob/master/kyaml/setters2/doc.go
	//
	//     {
	//        "definitions": {
	//          "io.k8s.cli.setters.replicas": {
	//            "x-k8s-cli": {
	//              "setter": {
	//                "name": "replicas",
	//                "value": "4"
	//              }
	//            }
	//          }
	//        }
	//      }
	//
	// (there are consts in kyaml/fieldmeta with the
	// prefixes).
	//
	// `fieldmeta.SetShortHandRef("$imagepolicy")` makes it possible
	// to just use (e.g.,)
	//
	//     image: foo:v1 # {"$imagepolicy": "automation-ns:foo"}
	//
	// to mark the fields at which to make replacements. A colon is
	// used to separate namespace and name in the key, because a slash
	// would be interpreted as part of the $ref path.
	imageRefs := make(map[string]imageRef)
	setAllCallback := func(file, setterName string, node *yaml.RNode) {
		ref, ok := imageRefs[setterName]
		if !ok {
			return
		}

		meta, err := node.GetMeta()
		if err != nil {
			return
		}
		oid := ObjectIdentifier{meta.GetIdentifier()}

		fileres, ok := result.Files[file]
		if !ok {
			fileres = FileResult{
				Objects: make(map[ObjectIdentifier][]ImageRef),
			}
			result.Files[file] = fileres
		}
		objres := fileres.Objects[oid]
		for _, n := range objres {
			if n == ref {
				return
			}
		}
		objres = append(objres, ref)
		fileres.Objects[oid] = objres
	}

	defs := map[string]spec.Schema{}
	for _, scan := range scans {
		if scan.Status.LatestImage == "" {
			continue
		}
		// Using strict validation would mean any image that omits the
		// registry would be rejected, so that can't be used
		// here. Using _weak_ validation means that defaults will be
		// filled in. Usually this would mean the tag would end up
		// being `latest` if empty in the input; but I'm assuming here
		// that the policy won't have a tagless ref.
		image := scan.Status.LatestImage
		r, err := name.ParseReference(image, name.WeakValidation)
		if err != nil {
			return fmt.Errorf("encountered invalid image ref %q: %w", scan.Status.LatestImage, err)
		}
		ref := imageRef{
			Reference: r,
			policy: types.NamespacedName{
				Name:      scan.Name,
				Namespace: scan.Namespace,
			},
		}
		tag := ref.Identifier()
		// annoyingly, neither the library imported above, nor an
		// alternative, I found will yield the original image name;
		// this is an easy way to get it
		name := image[:len(image)-len(tag)-1]

		imageSetter := scan.Spec.TagName
		defs[fieldmeta.SetterDefinitionPrefix+imageSetter] = setterSchema(imageSetter, scan.Status.LatestImage)
		imageRefs[imageSetter] = ref

		tagSetter := imageSetter + ":tag"
		defs[fieldmeta.SetterDefinitionPrefix+tagSetter] = setterSchema(tagSetter, tag)
		imageRefs[tagSetter] = ref

		// Context().Name() gives the image repository _as supplied_
		nameSetter := imageSetter + ":name"
		defs[fieldmeta.SetterDefinitionPrefix+nameSetter] = setterSchema(nameSetter, name)
		imageRefs[nameSetter] = ref

		digestSetter := imageSetter + ":digest"
		defs[fieldmeta.SetterDefinitionPrefix+digestSetter] = setterSchema(digestSetter, fmt.Sprintf("%s@%s", scan.Status.LatestImage, scan.Status.LatestDigest))
	}

	settersSchema.Definitions = defs
	set := &SetAllCallback{
		SettersSchema: &settersSchema,
	}

	// get ready with the reader and writer
	reader := &ScreeningLocalReader{
		Path:  inpath,
		Token: fmt.Sprintf("%q", SetterShortHand),
	}
	writer := &kio.LocalPackageWriter{
		PackagePath: outpath,
	}

	pipeline := kio.Pipeline{
		Inputs:  []kio.Reader{reader},
		Outputs: []kio.Writer{writer},
		Filters: []kio.Filter{
			setAll(set, setAllCallback),
		},
	}

	return pipeline.Execute()
}

// setAll returns a kio.Filter using the supplied SetAllCallback
// (dealing with individual nodes), amd calling the given callback
// whenever a field value is changed, and returning only nodes from
// files with changed nodes. This is based on
// [`SetAll`](https://github.com/kubernetes-sigs/kustomize/blob/kyaml/v0.10.16/kyaml/setters2/set.go#L503
// from kyaml/kio.
func setAll(filter *SetAllCallback, callback func(file, setterName string, node *yaml.RNode)) kio.Filter {
	return kio.FilterFunc(
		func(nodes []*yaml.RNode) ([]*yaml.RNode, error) {
			filesToUpdate := sets.Set[string]{}
			for i := range nodes {
				path, _, err := kioutil.GetFileAnnotations(nodes[i])
				if err != nil {
					return nil, err
				}

				filter.Callback = func(setter, oldValue, newValue string) {
					if newValue != oldValue {
						callback(path, setter, nodes[i])
						filesToUpdate.Insert(path)
					}
				}
				_, err = filter.Filter(nodes[i])
				if err != nil {
					return nil, err
				}
			}

			var nodesInUpdatedFiles []*yaml.RNode
			for i := range nodes {
				path, _, err := kioutil.GetFileAnnotations(nodes[i])
				if err != nil {
					return nil, err
				}
				if filesToUpdate.Has(path) {
					nodesInUpdatedFiles = append(nodesInUpdatedFiles, nodes[i])
				}
			}
			return nodesInUpdatedFiles, nil
		})
}

func setterSchema(name, value string) spec.Schema {
	schema := spec.StringProperty()
	schema.Extensions = map[string]interface{}{}
	schema.Extensions.Add(K8sCliExtensionKey, map[string]interface{}{
		"setter": map[string]string{
			"name":  name,
			"value": value,
		},
	})
	return *schema
}

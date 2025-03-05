package desiredset

import (
	"encoding/json"
	"slices"

	jsonpatch "github.com/evanphx/json-patch"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/diff"
	argo "github.com/rancher/fleet/internal/cmd/agent/deployer/internal/normalizers"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/resource"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/merr"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/normalizers"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Diff factors the bundledeployment's bundle diff patches into the plan from
// DryRun. This way, the status of the bundledeployment can be updated
// accurately.
func Diff(plan Plan, bd *fleet.BundleDeployment, ns string, objs ...runtime.Object) (Plan, error) {
	desired := objectset.NewObjectSet(objs...).ObjectsByGVK()
	live := objectset.NewObjectSet(plan.Objects...).ObjectsByGVK()

	norms, err := newNormalizers(live, bd)
	if err != nil {
		return plan, err
	}

	var errs []error
	if bd.Spec.Options.Diff != nil {
		toIgnore := objectset.ObjectKeyByGVK{}
		for _, patch := range bd.Spec.Options.Diff.ComparePatches {
			for _, op := range patch.Operations {
				gvk := schema.FromAPIVersionAndKind(patch.APIVersion, patch.Kind)

				if op.Op == fleet.IgnoreOp {
					key := objectset.ObjectKey{
						Name:      patch.Name,
						Namespace: patch.Namespace,
					}

					if _, ok := toIgnore[gvk]; !ok {
						toIgnore[gvk] = []objectset.ObjectKey{}
					}

					toIgnore[gvk] = append(toIgnore[gvk], key)
				}
			}
		}
		for gvk, objs := range plan.Create {
			if _, ok := toIgnore[gvk]; !ok {
				continue
			}
			for _, key := range objs {
				if idx := slices.Index(toIgnore[gvk], key); idx >= 0 {
					plan.Create[gvk] = slices.Delete(plan.Create[gvk], idx, idx+1)
					continue
				}
			}
		}
	}
	for gvk, objs := range plan.Update {
		for key := range objs {
			desiredObj := desired[gvk][key]
			if desiredObj == nil {
				desiredKey := key
				// if different namespace options to guess if resource is namespaced or not
				if desiredKey.Namespace == "" {
					desiredKey.Namespace = ns
				} else {
					desiredKey.Namespace = ""
				}
				desiredObj = desired[gvk][desiredKey]
				if desiredObj == nil {
					continue
				}
			}
			desiredObj.(*unstructured.Unstructured).SetNamespace(key.Namespace)

			actualObj := live[gvk][key]
			if actualObj == nil {
				continue
			}

			diffResult, err := diff.Diff(desiredObj.(*unstructured.Unstructured), actualObj.(*unstructured.Unstructured),
				diff.WithNormalizer(norms),
				diff.IgnoreAggregatedRoles(true))
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !diffResult.Modified {
				delete(plan.Update[gvk], key)
				continue
			}
			patch, err := jsonpatch.CreateMergePatch(diffResult.NormalizedLive, diffResult.PredictedLive)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			// this will overwrite an existing entry in the Update map
			plan.Update.Set(gvk, key.Namespace, key.Name, string(patch))
		}
		if len(errs) > 0 {
			return plan, merr.NewErrors(errs...)
		}
	}
	return plan, nil
}

// newNormalizers creates a normalizer that removes fields from resources.
// The normalizer is composed of:
//
//   - StatusNormalizer
//   - MutatingWebhookNormalizer
//   - ValidatingWebhookNormalizer
//   - normalizers.NewIgnoreNormalizer (patch.JsonPointers)
//   - normalizers.NewKnownTypesNormalizer (rollout.argoproj.io)
//   - patch.Operations
func newNormalizers(live objectset.ObjectByGVK, bd *fleet.BundleDeployment) (diff.Normalizer, error) {
	var ignore []resource.ResourceIgnoreDifferences
	jsonPatchNorm := &normalizers.JSONPatchNormalizer{}

	if bd.Spec.Options.Diff != nil {
		for _, patch := range bd.Spec.Options.Diff.ComparePatches {
			groupVersion, err := schema.ParseGroupVersion(patch.APIVersion)
			if err != nil {
				return nil, err
			}
			ignore = append(ignore, resource.ResourceIgnoreDifferences{
				Namespace:    patch.Namespace,
				Name:         patch.Name,
				Kind:         patch.Kind,
				Group:        groupVersion.Group,
				JSONPointers: patch.JsonPointers,
			})

			for _, op := range patch.Operations {
				// compile each operation by itself so that one failing operation doesn't block the others
				patchData, err := json.Marshal([]interface{}{op})
				if err != nil {
					return nil, err
				}

				if op.Op == fleet.IgnoreOp {
					continue
				}

				gvk := schema.FromAPIVersionAndKind(patch.APIVersion, patch.Kind)
				key := objectset.ObjectKey{
					Name:      patch.Name,
					Namespace: patch.Namespace,
				}
				jsonPatchNorm.Add(gvk, key, patchData)
			}
		}
	}

	ignoreNormalizer, err := argo.NewIgnoreNormalizer(ignore, nil)
	if err != nil {
		return nil, err
	}

	knownTypesNorm, err := argo.NewKnownTypesNormalizer(nil)
	if err != nil {
		return nil, err
	}

	return normalizers.New(live, ignoreNormalizer, knownTypesNorm, jsonPatchNorm), nil
}

package applied

import (
	"encoding/json"

	jsonpatch "github.com/evanphx/json-patch"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/diff"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/diffnormalize"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/resource"
	fleetnorm "github.com/rancher/fleet/internal/cmd/agent/deployer/normalizers"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/apply"
	"github.com/rancher/wrangler/v3/pkg/merr"
	"github.com/rancher/wrangler/v3/pkg/objectset"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Diff factors the bundledeployment's bundle diff patches into the plan from
// DryRun. This way, the status of the bundledeployment can be updated
// accurately.
func Diff(plan apply.Plan, bd *fleet.BundleDeployment, ns string, objs ...runtime.Object) (apply.Plan, error) {
	desired := objectset.NewObjectSet(objs...).ObjectsByGVK()
	live := objectset.NewObjectSet(plan.Objects...).ObjectsByGVK()

	norms, err := normalizers(live, bd)
	if err != nil {
		return plan, err
	}

	var errs []error
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
			plan.Update.Add(gvk, key.Namespace, key.Name, string(patch))
		}
		if len(errs) > 0 {
			return plan, merr.NewErrors(errs...)
		}
	}
	return plan, nil
}

func normalizers(live objectset.ObjectByGVK, bd *fleet.BundleDeployment) (diff.Normalizer, error) {
	var ignore []resource.ResourceIgnoreDifferences
	jsonPatchNorm := &fleetnorm.JSONPatchNormalizer{}
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
				gvk := schema.FromAPIVersionAndKind(patch.APIVersion, patch.Kind)
				key := objectset.ObjectKey{
					Name:      patch.Name,
					Namespace: patch.Namespace,
				}
				jsonPatchNorm.Add(gvk, key, patchData)
			}
		}
	}

	ignoreNorm, err := diffnormalize.NewDiffNormalizer(ignore, nil)
	if err != nil {
		return nil, err
	}

	norm := fleetnorm.New(live, ignoreNorm, jsonPatchNorm)
	return norm, nil
}

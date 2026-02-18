package desiredset

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/go-logr/logr"

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
func Diff(logger logr.Logger, plan Plan, bd *fleet.BundleDeployment, ns string, objs ...runtime.Object) (Plan, error) {
	desired := objectset.NewObjectSet(objs...).ObjectsByGVK()
	live := objectset.NewObjectSet(plan.Objects...).ObjectsByGVK()

	norms, err := newNormalizers(live, bd)
	if err != nil {
		return plan, err
	}

	var errs []error
	// Exclude ignored objects from set of objects to be created (plan.Create)
	if bd.Spec.Options.Diff != nil {
		toIgnore := map[schema.GroupVersionKind]map[objectset.ObjectKey]*regexp.Regexp{}

		for _, patch := range bd.Spec.Options.Diff.ComparePatches {
			for _, op := range patch.Operations {
				gvk := schema.FromAPIVersionAndKind(patch.APIVersion, patch.Kind)

				if op.Op == fleet.IgnoreOp {
					key := objectset.ObjectKey{
						Name:      patch.Name,
						Namespace: patch.Namespace,
					}

					if _, ok := toIgnore[gvk]; !ok {
						toIgnore[gvk] = map[objectset.ObjectKey]*regexp.Regexp{}
					}

					re, err := regexp.Compile(key.Name)
					if err != nil {
						// XXX: enable detection of such issues earlier, for instance through CLI validating
						// fleet.yaml syntax; see fleet#4533.
						logger.V(1).Error(
							err,
							"Cannot compile bundle diff ignore regex, will discard it",
							"namespace", key.Namespace,
							"name pattern", key.Name,
							"gvk", gvk.String(),
						)
						continue // this patch cannot be used
					}

					toIgnore[gvk][key] = re
				}
			}
		}

		for gvk := range plan.Create {
			if _, ok := toIgnore[gvk]; !ok {
				continue
			}

			plan.Create[gvk] = slices.DeleteFunc(plan.Create[gvk], func(o objectset.ObjectKey) bool {
				for k, re := range toIgnore[gvk] {
					// Match ignored objects by:
					// * [name + namespace] if both are specified in the patch
					//     * the match on the name can be exact, or regex-based (e.g. a patch with
					//       name `.*serv.*` would match `suse-observability`)
					// * namespace only if the patch provides the namespace alone
					switch {
					case k.Namespace != o.Namespace:
						continue
					case k.Name == "":
						fallthrough
					case k.Name == o.Name:
						return true // no need for further checks
					default:
						if re != nil && re.MatchString(o.Name) {
							return true
						}
					}

				}

				return false
			})
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

			uActual := actualObj.(*unstructured.Unstructured)

			diffResult, err := diff.Diff(desiredObj.(*unstructured.Unstructured), uActual,
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

			// Some normalization operations, unlike those called from Diff (vendored ArgoCD code), must only be applied
			// to actual, in-cluster objects, not to desired ones.
			emptied, err := normalizeActual(live, uActual, key, &patch)
			if err != nil {
				errs = append(errs, err)
				continue
			}

			if emptied {
				delete(plan.Update[gvk], key)
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

// normalizeActual encapsulates patch normalization operations which are only run against a live object (uActual),
// possibly requiring knowledge of other live resources.
func normalizeActual(
	live objectset.ObjectByGVK,
	uActual *unstructured.Unstructured,
	key objectset.ObjectKey,
	patch *[]byte,
) (bool, error) {
	emptied, err := normalizeReplicasPatch(live, uActual, key, patch)
	if err != nil || emptied {
		return emptied, err
	}

	return normalizeNullPatch(key, patch)
}

// normalizeReplicasPatch handles removal of a diff patch's `.spec.replicas` field on a Deployment or a StatefulSet.
// Processing involves checking whether live objects include HPAs referencing the object.
// Both v1 and v2 of the autoscaling API are supported.
// This can be called safely against any unstructured object: if the object turns out not to represent a Deployment nor a
// StatefulSet, this function is a no-op.
// Returns a boolean indicating whether the resulting patch is empty, and any error which may have happened in the
// process.
func normalizeReplicasPatch(
	live objectset.ObjectByGVK,
	uActual *unstructured.Unstructured,
	key objectset.ObjectKey,
	patch *[]byte,
) (bool, error) {
	actualGVK := uActual.GroupVersionKind()
	if actualGVK.Group != "apps" {
		return false, nil
	}

	if actualGVK.Kind != "Deployment" && actualGVK.Kind != "StatefulSet" {
		return false, nil
	}

	var patchData map[string]any
	if err := json.Unmarshal(*patch, &patchData); err != nil {
		return false, fmt.Errorf("failed to unmarshal patch for %s/%s: %v: %w", key.Namespace, key.Name, *patch, err)
	}

	patchSpec, patchHasSpec := patchData["spec"]
	if !patchHasSpec {
		// No need to even check HPAs for replicas
		return false, nil
	}

	// What differs between v1 and v2 is the set of supported metrics for scaling (with memory and custom metrics
	// included in v2); this is irrelevant to the logic at play here: we are only interested in values of replica
	// counts, not in what triggers their updates.
	supportedVersions := []string{"v2", "v1"}

	failFieldNotFound := func(k objectset.ObjectKey, fieldName string) error {
		return fmt.Errorf("malformed HPA %s/%s: field %q not found", k.Namespace, k.Name, fieldName)
	}

	// a non-nil error would mean that the field has an unexpected type; this cannot happen as per the Deployment
	// and StatefulSet APIs.
	actualReplicas, found, _ := unstructured.NestedInt64(uActual.Object, "spec", "replicas")
	if !found {
		return false, failFieldNotFound(key, ".spec.replicas")
	}

	for _, v := range supportedVersions {
		gvk := schema.GroupVersionKind{
			Group:   "autoscaling",
			Version: v,
			Kind:    "HorizontalPodAutoscaler",
		}

		for k, o := range live[gvk] {
			if k.Namespace != key.Namespace {
				continue
			}

			un, ok := o.(*unstructured.Unstructured)
			if !ok {
				continue
			}

			// in each case of extraction of HPA fields below, a non-nil error would mean that the field has
			// an unexpected type; this cannot happen as per the HPA API.
			minRepField, found, _ := unstructured.NestedInt64(un.Object, "spec", "minReplicas")
			if !found {
				minRepField = 1
			}

			maxRepField, found, _ := unstructured.NestedInt64(un.Object, "spec", "maxReplicas")
			if !found {
				return false, failFieldNotFound(k, ".spec.maxReplicas")
			}

			refObjField, found, _ := unstructured.NestedMap(un.Object, "spec", "scaleTargetRef")
			if !found {
				return false, failFieldNotFound(k, ".spec.scaleTargetRef")
			}

			// apiVersion can be empty
			refVersion, _, _ := unstructured.NestedString(refObjField, "apiVersion")

			refKind, found, _ := unstructured.NestedString(refObjField, "kind")
			if !found {
				return false, failFieldNotFound(k, ".spec.scaleTargetRef.kind")
			}

			refName, found, _ := unstructured.NestedString(refObjField, "name")
			if !found {
				return false, failFieldNotFound(k, ".spec.scaleTargetRef.name")
			}

			if refVersion != "" {
				groupVersion := strings.Split(refVersion, "/")
				var group, version string
				switch len(groupVersion) {
				case 1:
					version = groupVersion[0]
				case 2:
					group = groupVersion[0]
					version = groupVersion[1]
				default:
					continue
				}

				if actualGVK.Version != version || actualGVK.Group != group {
					continue
				}
			}

			if actualGVK.Kind != refKind {
				continue
			}

			if key.Name != refName {
				continue
			}

			if actualReplicas < minRepField || actualReplicas > maxRepField {
				return false, nil
			}

			// No need to check if the field actually exists, as we've been through that above.
			spec, ok := patchSpec.(map[string]any)
			if !ok {
				return false, fmt.Errorf("malformed spec for %s %s/%s", refKind, k.Namespace, k.Name)
			}
			delete(spec, "replicas")

			if len(patchData) == 1 /* spec only */ && len(spec) == 0 {
				// no more fields in the diff
				return true, nil
			}

			p, err := json.Marshal(patchData)
			if err != nil {
				return false, fmt.Errorf("failed to marshal patch after removing replicas field: %w", err)
			}

			*patch = p

			return false, nil
		}
	}

	return false, nil
}

// normalizeNullPatch removes null-valued fields from merge patches generated by
// diffing desired against live resources.
//
// Context: In merge patches, null means deleting a field. Helm v4 can emit explicit
// null values for omitted fields (e.g., spec.strategy.rollingUpdate: null) where
// Helm v3 omitted those fields entirely. When Kubernetes applies defaulting to live
// objects, this creates false drift: the patch wants to delete a field that was
// server-side defaulted.
//
// Solution: Strip all null-valued fields from patches. This preserves meaningful drift
// detection while ignoring defaulting-only differences, restoring Helm v3-like behavior.
//
// Returns true if the patch becomes empty after removing nulls (no real drift).
func normalizeNullPatch(
	key objectset.ObjectKey,
	patch *[]byte,
) (bool, error) {
	var patchData map[string]any
	if err := json.Unmarshal(*patch, &patchData); err != nil {
		// Format patch as string and truncate if too large for logging
		patchStr := string(*patch)
		const maxPatchLogLen = 1024
		if len(patchStr) > maxPatchLogLen {
			patchStr = patchStr[:maxPatchLogLen] + "...(truncated)"
		}
		return false, fmt.Errorf("failed to unmarshal patch for %s/%s: %q: %w", key.Namespace, key.Name, patchStr, err)
	}

	// Recursively remove all null values from the patch tree
	cleaned, _ := removeNullPatchFields(patchData).(map[string]any)
	if len(cleaned) == 0 {
		// Patch contained only nulls - no meaningful drift
		return true, nil
	}

	p, err := json.Marshal(cleaned)
	if err != nil {
		return false, fmt.Errorf("failed to marshal patch after removing null fields: %w", err)
	}

	*patch = p
	return false, nil
}

// removeNullPatchFields recursively removes null values from patch data structures.
// It handles both maps (objects) and slices (arrays), preserving non-null values.
//
// Map behavior:
//   - Skips entries with nil values
//   - Skips entries that become empty maps after cleaning
//
// Slice behavior:
//   - Skips nil elements
//   - Skips elements that become empty maps after cleaning
//   - Preserves non-map elements (strings, numbers, etc.)
func removeNullPatchFields(value any) any {
	switch v := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			// Skip null values in maps
			if item == nil {
				continue
			}

			// Recursively clean nested structures
			cleaned := removeNullPatchFields(item)

			// Skip maps that became empty after cleaning (contained only nulls)
			if cleanedMap, ok := cleaned.(map[string]any); ok && len(cleanedMap) == 0 {
				continue
			}

			result[key] = cleaned
		}
		return result

	case []any:
		result := make([]any, 0, len(v))
		for i := range v {
			// Skip null elements in arrays
			if v[i] == nil {
				continue
			}

			// Recursively clean nested structures
			cleaned := removeNullPatchFields(v[i])

			// Skip maps that became empty after cleaning
			if cleanedMap, ok := cleaned.(map[string]any); ok && len(cleanedMap) == 0 {
				continue
			}

			result = append(result, cleaned)
		}
		return result

	default:
		// Leaf values (strings, numbers, bools) pass through unchanged
		return v
	}
}

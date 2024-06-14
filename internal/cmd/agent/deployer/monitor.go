package deployer

import (
	"encoding/json"
	"fmt"
	"sort"

	jsonpatch "github.com/evanphx/json-patch"

	"github.com/pkg/errors"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/diff"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/diffnormalize"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/resource"
	fleetnorm "github.com/rancher/fleet/internal/cmd/agent/deployer/normalizers"
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/apply"
	"github.com/rancher/wrangler/v2/pkg/merr"
	"github.com/rancher/wrangler/v2/pkg/objectset"
	"github.com/rancher/wrangler/v2/pkg/summary"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// plan first does a dry run of the apply to get the difference between the
// desired and live state. It relies on the bundledeployment's bundle diff
// patches to ignore changes.
func (m *Manager) plan(bd *fleet.BundleDeployment, ns string, objs ...runtime.Object) (apply.Plan, error) {
	if ns == "" {
		ns = m.defaultNamespace
	}

	a := m.getApply(bd, ns)
	plan, err := a.DryRun(objs...)
	if err != nil {
		return plan, err
	}

	desired := objectset.NewObjectSet(objs...).ObjectsByGVK()
	live := objectset.NewObjectSet(plan.Objects...).ObjectsByGVK()

	norms, err := m.normalizers(live, bd)
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

func (m *Manager) normalizers(live objectset.ObjectByGVK, bd *fleet.BundleDeployment) (diff.Normalizer, error) {
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

func (m *Manager) getApply(bd *fleet.BundleDeployment, ns string) apply.Apply {
	apply := m.apply
	return apply.
		WithIgnorePreviousApplied().
		WithSetID(helmdeployer.GetSetID(bd.Name, m.labelPrefix, m.labelSuffix)).
		WithDefaultNamespace(ns)
}

// UpdateBundleDeploymentStatus updates the status with information from the
// helm release history and an apply dry run.
func (m *Manager) UpdateBundleDeploymentStatus(mapper meta.RESTMapper, bd *fleet.BundleDeployment) error {
	resources, err := m.deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		return err
	}
	resourcesPreviousRelease, err := m.deployer.ResourcesFromPreviousReleaseVersion(bd.Name, bd.Status.Release)
	if err != nil {
		return err
	}

	plan, err := m.plan(bd, resources.DefaultNamespace, resources.Objects...)
	if err != nil {
		return err
	}

	bd.Status.NonReadyStatus = nonReady(plan, bd.Spec.Options.IgnoreOptions)
	bd.Status.ModifiedStatus = modified(plan, resourcesPreviousRelease)
	bd.Status.Ready = false
	bd.Status.NonModified = false

	if len(bd.Status.NonReadyStatus) == 0 {
		bd.Status.Ready = true
	}
	if len(bd.Status.ModifiedStatus) == 0 {
		bd.Status.NonModified = true
	} else if bd.Spec.CorrectDrift != nil && bd.Spec.CorrectDrift.Enabled {
		release, err := m.deployer.RemoveExternalChanges(bd)
		if err != nil {
			// Update BundleDeployment status as wrangler doesn't update the status if error is not nil.
			_, errStatus := m.bundleDeploymentController.UpdateStatus(bd)
			if errStatus != nil {
				return errors.Wrap(err, "error updating status when reconciling drift: "+errStatus.Error())
			}
			return errors.Wrapf(err, "error reconciling drift")
		}
		bd.Status.Release = release
	}

	bd.Status.Resources = []fleet.BundleDeploymentResource{}
	for _, obj := range plan.Objects {
		m, err := meta.Accessor(obj)
		if err != nil {
			return err
		}

		ns := m.GetNamespace()
		gvk := obj.GetObjectKind().GroupVersionKind()
		if ns == "" && isNamespaced(mapper, gvk) {
			ns = resources.DefaultNamespace
		}

		version, kind := gvk.ToAPIVersionAndKind()
		bd.Status.Resources = append(bd.Status.Resources, fleet.BundleDeploymentResource{
			Kind:       kind,
			APIVersion: version,
			Namespace:  ns,
			Name:       m.GetName(),
			CreatedAt:  m.GetCreationTimestamp(),
		})
	}

	return nil
}

func isNamespaced(mapper meta.RESTMapper, gvk schema.GroupVersionKind) bool {
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return true
	}
	return mapping.Scope.Name() == meta.RESTScopeNameNamespace
}

func sortKey(f fleet.ModifiedStatus) string {
	return f.APIVersion + "/" + f.Kind + "/" + f.Namespace + "/" + f.Name
}

func modified(plan apply.Plan, resourcesPreviousRelease *helmdeployer.Resources) (result []fleet.ModifiedStatus) {
	defer func() {
		sort.Slice(result, func(i, j int) bool {
			return sortKey(result[i]) < sortKey(result[j])
		})
	}()
	for gvk, keys := range plan.Create {
		for _, key := range keys {
			if len(result) >= 10 {
				return result
			}

			apiVersion, kind := gvk.ToAPIVersionAndKind()
			result = append(result, fleet.ModifiedStatus{
				Kind:       kind,
				APIVersion: apiVersion,
				Namespace:  key.Namespace,
				Name:       key.Name,
				Create:     true,
			})
		}
	}

	for gvk, keys := range plan.Delete {
		for _, key := range keys {
			if len(result) >= 10 {
				return result
			}

			apiVersion, kind := gvk.ToAPIVersionAndKind()
			// Check if resource was in a previous release. It is possible that some operators copy the
			// objectset.rio.cattle.io/hash label into a dynamically created objects. We need to skip these resources
			// because they are not part of the release, and they would appear as orphaned.
			// https://github.com/rancher/fleet/issues/1141
			if isResourceInPreviousRelease(key, kind, resourcesPreviousRelease.Objects) {
				result = append(result, fleet.ModifiedStatus{
					Kind:       kind,
					APIVersion: apiVersion,
					Namespace:  key.Namespace,
					Name:       key.Name,
					Delete:     true,
				})
			}
		}
	}

	for gvk, patches := range plan.Update {
		for key, patch := range patches {
			if len(result) >= 10 {
				break
			}

			apiVersion, kind := gvk.ToAPIVersionAndKind()
			result = append(result, fleet.ModifiedStatus{
				Kind:       kind,
				APIVersion: apiVersion,
				Namespace:  key.Namespace,
				Name:       key.Name,
				Patch:      patch,
			})
		}
	}

	return result
}

func isResourceInPreviousRelease(key objectset.ObjectKey, kind string, objsPreviousRelease []runtime.Object) bool {
	for _, obj := range objsPreviousRelease {
		metadata, _ := meta.Accessor(obj)
		if obj.GetObjectKind().GroupVersionKind().Kind == kind && metadata.GetName() == key.Name {
			return true
		}
	}

	return false
}

func nonReady(plan apply.Plan, ignoreOptions fleet.IgnoreOptions) (result []fleet.NonReadyStatus) {
	defer func() {
		sort.Slice(result, func(i, j int) bool {
			return result[i].UID < result[j].UID
		})
	}()

	for _, obj := range plan.Objects {
		if len(result) >= 10 {
			return result
		}
		if u, ok := obj.(*unstructured.Unstructured); ok {
			if ignoreOptions.Conditions != nil {
				if err := excludeIgnoredConditions(u, ignoreOptions); err != nil {
					logrus.Errorf("failed to ignore conditions: %v", err)
				}
			}

			summary := summary.Summarize(u)
			if !summary.IsReady() {
				result = append(result, fleet.NonReadyStatus{
					UID:        u.GetUID(),
					Kind:       u.GetKind(),
					APIVersion: u.GetAPIVersion(),
					Namespace:  u.GetNamespace(),
					Name:       u.GetName(),
					Summary:    summary,
				})
			}
		}
	}

	return result
}

// excludeIgnoredConditions removes the conditions that are included in ignoreOptions from the object passed as a parameter
func excludeIgnoredConditions(obj *unstructured.Unstructured, ignoreOptions fleet.IgnoreOptions) error {
	conditions, _, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil {
		return err
	}
	conditionsWithoutIgnored := make([]interface{}, 0)

	for _, condition := range conditions {
		condition, ok := condition.(map[string]interface{})
		if !ok {
			return fmt.Errorf("condition: %#v can't be converted to map[string]interface{}", condition)
		}
		excludeCondition := false
		for _, ignoredCondition := range ignoreOptions.Conditions {
			if shouldExcludeCondition(condition, ignoredCondition) {
				excludeCondition = true
				break
			}
		}
		if !excludeCondition {
			conditionsWithoutIgnored = append(conditionsWithoutIgnored, condition)
		}
	}

	err = unstructured.SetNestedSlice(obj.Object, conditionsWithoutIgnored, "status", "conditions")
	if err != nil {
		return err
	}

	return nil
}

// shouldExcludeCondition returns true if all the elements of ignoredConditions are inside conditions
func shouldExcludeCondition(conditions map[string]interface{}, ignoredConditions map[string]string) bool {
	if len(ignoredConditions) > len(conditions) {
		return false
	}

	for k, v := range ignoredConditions {
		if vc, found := conditions[k]; !found || vc != v {
			return false
		}
	}

	return true
}

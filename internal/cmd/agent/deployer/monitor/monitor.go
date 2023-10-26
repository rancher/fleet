package monitor

import (
	"fmt"
	"sort"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/plan"
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v2/pkg/apply"
	"github.com/rancher/wrangler/v2/pkg/objectset"
	"github.com/rancher/wrangler/v2/pkg/summary"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Monitor struct {
	defaultNamespace           string
	deployer                   *helmdeployer.Helm
	apply                      apply.Apply
	labelPrefix                string
	labelSuffix                string
	bundleDeploymentController fleetcontrollers.BundleDeploymentController
}

func New(defaultNamespace string,
	labelPrefix, labelSuffix string,
	deployer *helmdeployer.Helm,
	apply apply.Apply) *Monitor {
	return &Monitor{
		defaultNamespace: defaultNamespace,
		labelPrefix:      labelPrefix,
		labelSuffix:      labelSuffix,
		deployer:         deployer,
		apply:            apply.WithDynamicLookup(),
	}
}

// UpdateBundleDeploymentStatus updates the status with information from the
// helm release history and an apply dry run.
func (m *Monitor) UpdateBundleDeploymentStatus(mapper meta.RESTMapper, bd *fleet.BundleDeployment) error {
	resources, err := m.deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		return err
	}
	resourcesPreviousRelease, err := m.deployer.ResourcesFromPreviousReleaseVersion(bd.Name, bd.Status.Release)
	if err != nil {
		return err
	}

	ns := resources.DefaultNamespace
	if ns == "" {
		ns = m.defaultNamespace
	}
	apply := plan.GetApply(m.apply, plan.Options{
		LabelPrefix:      m.labelPrefix,
		LabelSuffix:      m.labelSuffix,
		DefaultNamespace: ns,
		Name:             bd.Name,
	})

	plan, err := plan.Plan(apply, bd, resources.DefaultNamespace, resources.Objects...)
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
	} else if bd.Spec.CorrectDrift.Enabled {
		err = m.deployer.RemoveExternalChanges(bd)
		if err != nil {
			// Update BundleDeployment status as wrangler doesn't update the status if error is not nil.
			_, errStatus := m.bundleDeploymentController.UpdateStatus(bd)
			if errStatus != nil {
				return errors.Wrap(err, "error updating status when reconciling drift: "+errStatus.Error())
			}
			return errors.Wrapf(err, "error reconciling drift")
		}
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

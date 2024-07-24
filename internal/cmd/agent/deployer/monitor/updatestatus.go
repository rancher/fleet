// Package monitor provides functionality for monitoring and updating the status of a bundle deployment.
// It includes functions for determining whether the agent should be redeployed, whether the status should be updated,
// and for updating the status based on the resources and helm release history.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	"github.com/rancher/wrangler/v3/pkg/apply"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/objectset"
	"github.com/rancher/wrangler/v3/pkg/summary"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/applied"
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

type Monitor struct {
	client  client.Client
	applied *applied.Applied

	deployer *helmdeployer.Helm

	defaultNamespace string
	labelPrefix      string
	labelSuffix      string
}

func New(client client.Client, applied *applied.Applied, deployer *helmdeployer.Helm, defaultNamespace string, labelSuffix string) *Monitor {
	return &Monitor{
		client:           client,
		applied:          applied,
		deployer:         deployer,
		defaultNamespace: defaultNamespace,
		labelPrefix:      defaultNamespace,
		labelSuffix:      labelSuffix,
	}
}

func ShouldRedeployAgent(bd *fleet.BundleDeployment) bool {
	if isAgent(bd) {
		return true
	}
	if bd.Spec.Options.ForceSyncGeneration <= 0 {
		return false
	}
	if bd.Status.SyncGeneration == nil {
		return true
	}
	return *bd.Status.SyncGeneration != bd.Spec.Options.ForceSyncGeneration
}

func isAgent(bd *fleet.BundleDeployment) bool {
	return strings.HasPrefix(bd.Name, "fleet-agent")
}

func ShouldUpdateStatus(bd *fleet.BundleDeployment) bool {
	if bd.Spec.DeploymentID != bd.Status.AppliedDeploymentID {
		return false
	}

	// If the bundle failed to install the status should not be updated. Updating
	// here would remove the condition message that was previously set on it.
	if condition.Cond(fleet.BundleDeploymentConditionInstalled).IsFalse(bd) {
		return false
	}

	return true
}

func (m *Monitor) UpdateStatus(ctx context.Context, bd *fleet.BundleDeployment, resources *helmdeployer.Resources) (fleet.BundleDeploymentStatus, error) {
	logger := log.FromContext(ctx).WithName("UpdateStatus")

	// updateFromResources mutates bd.Status, so copy it first
	origStatus := *bd.Status.DeepCopy()
	bd = bd.DeepCopy()
	err := m.updateFromResources(logger, bd, resources)
	if err != nil {

		// Returning an error will cause UpdateStatus to requeue in a loop.
		// When there is no resourceID the error should be on the status. Without
		// the ID we do not have the information to lookup the resources to
		// compute the plan and discover the state of resources.
		if err == helmdeployer.ErrNoResourceID {
			return origStatus, nil
		}

		return origStatus, err
	}
	status := bd.Status

	readyError := readyError(status)
	condition.Cond(fleet.BundleDeploymentConditionReady).SetError(&status, "", readyError)

	status.SyncGeneration = &bd.Spec.Options.ForceSyncGeneration
	if readyError != nil {
		logger.Info("Status not ready", "error", readyError)
	}

	removePrivateFields(&status)
	return status, nil
}

// removePrivateFields removes fields from the status, which won't be marshalled to JSON.
// They would however trigger a status update in apply
func removePrivateFields(s1 *fleet.BundleDeploymentStatus) {
	for id := range s1.NonReadyStatus {
		s1.NonReadyStatus[id].Summary.Relationships = nil
		s1.NonReadyStatus[id].Summary.Attributes = nil
	}
}

// readyError returns an error based on the provided status.
// That error is non-nil if the status corresponds to a non-ready or modified state of the bundle deployment.
func readyError(status fleet.BundleDeploymentStatus) error {
	if status.Ready && status.NonModified {
		return nil
	}

	var msg string
	if !status.Ready {
		msg = "not ready"
		if len(status.NonReadyStatus) > 0 {
			msg = status.NonReadyStatus[0].String()
		}
	} else if !status.NonModified {
		msg = "out of sync"
		if len(status.ModifiedStatus) > 0 {
			msg = status.ModifiedStatus[0].String()
		}
	}

	return errors.New(msg)
}

// updateFromResources updates the status with information from the
// helm release history and an apply dry run.
func (m *Monitor) updateFromResources(logger logr.Logger, bd *fleet.BundleDeployment, resources *helmdeployer.Resources) error {
	resourcesPreviousRelease, err := m.deployer.ResourcesFromPreviousReleaseVersion(bd.Name, bd.Status.Release)
	if err != nil {
		return err
	}

	ns := resources.DefaultNamespace
	if ns == "" {
		ns = m.defaultNamespace
	}

	// resources.Objects contains the desired state of the resources from helm history
	plan, err := m.applied.DryRun(ns, applied.GetSetID(bd.Name, m.labelPrefix, m.labelSuffix), resources.Objects...)
	if err != nil {
		return err
	}

	// applied.Diff only takes plan.Update into account. plan.Update
	// contains objects which have changes to existing values. Adding a new
	// key to a map is not considered an update.
	plan, err = applied.Diff(plan, bd, resources.DefaultNamespace, resources.Objects...)
	if err != nil {
		return err
	}

	bd.Status.NonReadyStatus = nonReady(logger, plan, bd.Spec.Options.IgnoreOptions)
	bd.Status.ModifiedStatus = modified(m.client, plan, resourcesPreviousRelease)
	bd.Status.Ready = false
	bd.Status.NonModified = false

	if len(bd.Status.NonReadyStatus) == 0 {
		bd.Status.Ready = true
	}
	if len(bd.Status.ModifiedStatus) == 0 {
		bd.Status.NonModified = true
	}

	bd.Status.Resources = []fleet.BundleDeploymentResource{}
	for _, obj := range plan.Objects {
		ma, err := meta.Accessor(obj)
		if err != nil {
			return err
		}

		ns := ma.GetNamespace()
		gvk := obj.GetObjectKind().GroupVersionKind()
		if ns == "" && isNamespaced(m.client.RESTMapper(), gvk) {
			ns = resources.DefaultNamespace
		}

		version, kind := gvk.ToAPIVersionAndKind()
		bd.Status.Resources = append(bd.Status.Resources, fleet.BundleDeploymentResource{
			Kind:       kind,
			APIVersion: version,
			Namespace:  ns,
			Name:       ma.GetName(),
			CreatedAt:  ma.GetCreationTimestamp(),
		})
	}

	return nil
}

func nonReady(logger logr.Logger, plan apply.Plan, ignoreOptions fleet.IgnoreOptions) (result []fleet.NonReadyStatus) {
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
					logger.Error(err, "failed to ignore conditions")
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

// modified returns a list of modified statuses based on the provided plan and previous release resources.
// The function iterates through the plan's create, delete, and update actions and constructs a modified status
// for each resource.
// If the number of modified statuses exceeds 10, the function stops and returns the current result.
func modified(c client.Client, plan apply.Plan, resourcesPreviousRelease *helmdeployer.Resources) (result []fleet.ModifiedStatus) {
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

			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(gvk)
			key := client.ObjectKey{
				Namespace: key.Namespace,
				Name:      key.Name,
			}
			err := c.Get(context.Background(), key, obj)

			result = append(result, fleet.ModifiedStatus{
				Kind:       kind,
				APIVersion: apiVersion,
				Namespace:  key.Namespace,
				Name:       key.Name,
				Create:     true,
				Exist:      err == nil,
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

package deployer

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/argoproj/argo-cd/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/util/argo"
	"github.com/argoproj/gitops-engine/pkg/diff"
	jsonpatch "github.com/evanphx/json-patch"
	fleetnorm "github.com/rancher/fleet/modules/agent/pkg/deployer/normalizers"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/merr"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/objectset"
	"github.com/rancher/wrangler/pkg/summary"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type DeploymentStatus struct {
	Ready          bool                   `json:"ready,omitempty"`
	NonModified    bool                   `json:"nonModified,omitempty"`
	NonReadyStatus []fleet.NonReadyStatus `json:"nonReadyStatus,omitempty"`
	ModifiedStatus []fleet.ModifiedStatus `json:"modifiedStatus,omitempty"`
}

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
	var ignore []v1alpha1.ResourceIgnoreDifferences
	jsonPatchNorm := &fleetnorm.JSONPatchNormalizer{}
	if bd.Spec.Options.Diff != nil {
		for _, patch := range bd.Spec.Options.Diff.ComparePatches {
			groupVersion, err := schema.ParseGroupVersion(patch.APIVersion)
			if err != nil {
				return nil, err
			}
			ignore = append(ignore, v1alpha1.ResourceIgnoreDifferences{
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

	ignoreNorm, err := argo.NewDiffNormalizer(ignore, nil)
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
		WithSetID(GetSetID(bd.Name, m.labelPrefix, m.labelSuffix)).
		WithDefaultNamespace(ns)
}

func (m *Manager) MonitorBundle(bd *fleet.BundleDeployment) (DeploymentStatus, error) {
	var status DeploymentStatus

	resources, err := m.deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		return status, err
	}

	plan, err := m.plan(bd, resources.DefaultNamespace, resources.Objects...)
	if err != nil {
		return status, err
	}

	status.NonReadyStatus = nonReady(plan)
	status.ModifiedStatus = modified(plan)
	status.Ready = false
	status.NonModified = false

	if len(status.NonReadyStatus) == 0 {
		status.Ready = true
	}
	if len(status.ModifiedStatus) == 0 {
		status.NonModified = true
	}

	return status, nil
}

func GetSetID(bundleID, labelPrefix, labelSuffix string) string {
	// bundle is fleet-agent bundle, we need to use setID fleet-agent-bootstrap since it was applied with import controller
	if strings.HasPrefix(bundleID, "fleet-agent") {
		if labelSuffix == "" {
			return "fleet-agent-bootstrap"
		}
		return name.SafeConcatName("fleet-agent-bootstrap", labelSuffix)
	}
	if labelSuffix != "" {
		return name.SafeConcatName(labelPrefix, bundleID, labelSuffix)
	}
	return name.SafeConcatName(labelPrefix, bundleID)
}

func sortKey(f fleet.ModifiedStatus) string {
	return f.APIVersion + "/" + f.Kind + "/" + f.Namespace + "/" + f.Name
}

func modified(plan apply.Plan) (result []fleet.ModifiedStatus) {
	defer func() {
		sort.Slice(result, func(i, j int) bool {
			return sortKey(result[i]) < sortKey(result[j])
		})
	}()
	for gvk, keys := range plan.Create {
		for _, key := range keys {
			if len(result) >= 10 {
				return
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
				return
			}

			apiVersion, kind := gvk.ToAPIVersionAndKind()
			result = append(result, fleet.ModifiedStatus{
				Kind:       kind,
				APIVersion: apiVersion,
				Namespace:  key.Namespace,
				Name:       key.Name,
				Delete:     true,
			})
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

func nonReady(plan apply.Plan) (result []fleet.NonReadyStatus) {
	defer func() {
		sort.Slice(result, func(i, j int) bool {
			return result[i].UID < result[j].UID
		})
	}()

	for _, obj := range plan.Objects {
		if len(result) >= 10 {
			return
		}
		if u, ok := obj.(*unstructured.Unstructured); ok {
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

	return
}

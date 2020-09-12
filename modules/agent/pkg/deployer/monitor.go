package deployer

import (
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/summary"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type DeploymentStatus struct {
	Ready          bool                   `json:"ready,omitempty"`
	NonModified    bool                   `json:"nonModified,omitempty"`
	NonReadyStatus []fleet.NonReadyStatus `json:"nonReadyStatus,omitempty"`
	ModifiedStatus []fleet.ModifiedStatus `json:"modifiedStatus,omitempty"`
}

func (m *Manager) getApply(bd *fleet.BundleDeployment, ns string) apply.Apply {
	if ns == "" {
		ns = m.defaultNamespace
	}
	return m.apply.
		WithIgnorePreviousApplied().
		WithSetID(name.SafeConcatName(m.labelPrefix, bd.Name)).
		WithDefaultNamespace(ns)
}

func (m *Manager) MonitorBundle(bd *fleet.BundleDeployment) (DeploymentStatus, error) {
	var status DeploymentStatus

	resources, err := m.deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		return status, nil
	}

	plan, err := m.getApply(bd, resources.DefaultNamespace).
		DryRun(resources.Objects...)
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

	return
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

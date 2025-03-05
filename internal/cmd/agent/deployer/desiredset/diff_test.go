package desiredset_test

import (
	"testing"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func Test_Diff_IgnoreResources(t *testing.T) {
	ns := "fleet-local"

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "bar",
		Kind:    "foo",
	}
	plan := desiredset.Plan{
		Create: objectset.ObjectKeyByGVK{
			gvk: []objectset.ObjectKey{
				{
					Name:      "baz",
					Namespace: ns,
				},
				{
					Name:      "other", // should be left untouched, not ignored
					Namespace: ns,
				},
			},
		},
	}
	bd := v1alpha1.BundleDeployment{
		Spec: v1alpha1.BundleDeploymentSpec{
			Options: v1alpha1.BundleDeploymentOptions{
				Diff: &v1alpha1.DiffOptions{
					ComparePatches: []v1alpha1.ComparePatch{
						{
							Kind:       "foo",
							APIVersion: "bar",
							Namespace:  ns,
							Name:       "baz",
							Operations: []v1alpha1.Operation{
								{
									Op: "ignore",
								},
							},
						},
					},
				},
			},
		},
	}

	objs := []runtime.Object{}

	lenBefore := len(plan.Create[gvk])

	_, err := desiredset.Diff(plan, &bd, ns, objs...)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(plan.Create[gvk]) != lenBefore-1 {
		t.Errorf("unexpected plan.Create length: expected %d, got %d", lenBefore-1, len(plan.Create[gvk]))
	}
}

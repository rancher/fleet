//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/logger_mock.go -package=mocks github.com/go-logr/logr/ LogSink
package desiredset_test

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func Test_Diff_IgnoreResources(t *testing.T) {
	ns := "fleet-local"
	ns2 := "other-ns"
	ns3 := "yet-another-ns"

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
					Name:      "other",
					Namespace: ns2,
				},
				{
					Name:      "other", // should be left untouched, not ignored
					Namespace: ns,
				},
				{
					Name:      "suse-observability-one",
					Namespace: ns3,
				},
				{
					Name:      "blah",
					Namespace: ns2,
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
						{
							Kind:       "foo",
							APIVersion: "bar",
							Namespace:  ns2,
							// No name specified here: should match all resources of this kind in this namespace.
							Operations: []v1alpha1.Operation{
								{
									Op: "ignore",
								},
							},
						},
						{
							Kind:       "foo",
							APIVersion: "bar",
							Namespace:  ns3,
							Name:       "*obs*", // invalid regex; should not break anything
							Operations: []v1alpha1.Operation{
								{
									Op: "ignore",
								},
							},
						},
						{
							Kind:       "foo",
							APIVersion: "bar",
							Namespace:  ns3,
							Name:       ".*obs.*",
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

	ctrl := gomock.NewController(t)
	mockLogSink := mocks.NewMockLogSink(ctrl)
	mockLogSink.EXPECT().Init(gomock.Any())
	mockLogSink.EXPECT().Enabled(gomock.Any()).Return(true).AnyTimes()
	mockLogSink.EXPECT().Error(gomock.Any(), gomock.Any(), gomock.Any()).Times(1) // caused by invalid regex

	logger := logr.New(mockLogSink)

	_, err := desiredset.Diff(logger, plan, &bd, ns, objs...)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	shouldBeIgnored := 4

	if len(plan.Create[gvk]) != lenBefore-shouldBeIgnored {
		t.Errorf("unexpected plan.Create length: expected %d, got %d", lenBefore-shouldBeIgnored, len(plan.Create[gvk]))
		t.Errorf("got elements %v", plan.Create[gvk])
	}
}

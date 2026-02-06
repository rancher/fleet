//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/logger_mock.go -package=mocks github.com/go-logr/logr/ LogSink
package desiredset_test

import (
	"maps"
	"testing"

	"github.com/go-logr/logr"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
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

func Test_Diff_HPA(t *testing.T) {
	gvkDeploy := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}
	gvkStatefulSet := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "StatefulSet",
	}

	testCases := []struct {
		name         string
		releaseObj   []runtime.Object
		plan         desiredset.Plan
		expectedPlan desiredset.Plan
	}{
		{
			name: "statefulset with diff is referenced by v2 HPA and has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "StatefulSet", "nginx"),
				statefulSet("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkStatefulSet: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "StatefulSet", "nginx"),
					// different replica count from release, but still within HPA's interval
					statefulSet("my-ns", "nginx", 4),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkStatefulSet: map[objectset.ObjectKey]string{},
				},
			},
		},
		{
			// Empty min replica should be interpreted as 1
			name: "deployment with diff is referenced by v2 HPA with no min replica and has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", nil, 1, "apps/v1", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 1),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":1}}`},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", nil, 1, "apps/v1", "Deployment", "nginx"),
					// 0 replicas do not make much sense functionally, but for the sake of testing
					// differences with 1 being the only replica count allowed by the HPA
					deployment("my-ns", "nginx", 0),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":1}}`, // 0 is not within the interval of allowed values
					},
				},
			},
		},
		{
			name: "deployment with diff is referenced by v2 HPA with empty API version and has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "", "Deployment", "nginx"),
					// different replica count from release, but still within HPA's interval
					deployment("my-ns", "nginx", 4),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{},
				},
			},
		},
		{
			name: "deployment with multiple fields in diff is referenced by v2 HPA with empty API version and has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3, func(d *appsv1.Deployment) { d.Spec.MinReadySeconds = 42 }),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3,"minReadySeconds":42}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "", "Deployment", "nginx"),
					// different replica count from release, but still within HPA's interval
					deployment("my-ns", "nginx", 4, func(d *appsv1.Deployment) { d.Spec.MinReadySeconds = 12 }),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"minReadySeconds":42}}`, // only replicas should be normalised if within the allowed interval
					},
				},
			},
		},
		{
			name: "v2 HPA references another deployment (wrong API version) although deployment has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v2", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v2", "Deployment", "nginx"),
					// different replica count from release, but still within HPA's interval
					deployment("my-ns", "nginx", 4),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
			},
		},
		{
			name: "v2 HPA references another deployment (wrong kind) although deployment has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "NotADeployment", "nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "NotADeployment", "nginx"),
					// different replica count from release, but still within HPA's interval
					deployment("my-ns", "nginx", 4),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
			},
		},
		{
			name: "v2 HPA references another deployment (wrong name) although deployment has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "NotADeployment", "not-nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "NotADeployment", "not-nginx"),
					// different replica count from release, but still within HPA's interval
					deployment("my-ns", "nginx", 4),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
			},
		},
		{
			name: "deployment with diff is referenced by v2 HPA and has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
					// different replica count from release, but still within HPA's interval
					deployment("my-ns", "nginx", 4),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{},
				},
			},
		},
		{
			name: "deployment with diff is referenced by v1 HPA and has replicas within HPA's interval",
			releaseObj: []runtime.Object{
				hpav1("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpav1("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
					// different replica count from release, but still within HPA's interval
					deployment("my-ns", "nginx", 4),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{},
				},
			},
		},
		{
			name: "deployment with diff is referenced by HPA and has replicas below HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
					deployment("my-ns", "nginx", 1),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
			},
		},
		{
			name: "deployment with diff is referenced by HPA and has replicas above HPA's interval",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
					deployment("my-ns", "nginx", 6),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
			},
		},
		{
			name: "deployment with diff has replicas within HPA's interval but HPA lives in another namespace",
			releaseObj: []runtime.Object{
				hpa("my-other-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-other-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
					deployment("my-ns", "nginx", 6),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
			},
		},
		{
			name: "deployment with diff has replicas within HPA's interval and diff does not affect spec",
			releaseObj: []runtime.Object{
				hpa("my-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
				deployment("my-ns", "nginx", 3, func(d *appsv1.Deployment) {
					d.Labels = make(map[string]string)
					d.Labels["foo"] = "bar"
				}),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"metadata":{"labels":{"foo":"bar"}}}`,
					},
				},
				Objects: []runtime.Object{
					hpa("my-other-ns", "my-hpa", ptr.To(int32(2)), 5, "apps/v1", "Deployment", "nginx"),
					deployment("my-ns", "nginx", 3, func(d *appsv1.Deployment) {
						d.Labels = make(map[string]string)
						d.Labels["foo"] = "new-value"
					}),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"metadata":{"labels":{"foo":"bar"}}}`,
					},
				},
			},
		},
		{
			name: "deployment has diff and no HPAs exist",
			releaseObj: []runtime.Object{
				deployment("my-ns", "nginx", 3),
			},
			plan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
				Objects: []runtime.Object{
					deployment("my-ns", "nginx", 6),
				},
			},
			expectedPlan: desiredset.Plan{
				Update: desiredset.PatchByGVK{
					gvkDeploy: map[objectset.ObjectKey]string{
						{
							Name:      "nginx",
							Namespace: "my-ns",
						}: `{"spec":{"replicas":3}}`,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ns := "fleet-local"

			plan := tc.plan

			objects := []runtime.Object{}
			for _, ro := range tc.releaseObj {
				obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ro)
				if err != nil {
					t.Errorf("failed to convert release obj into unstructured: %v; %v", ro, err)
				}
				uObj := &unstructured.Unstructured{Object: obj}

				objects = append(objects, uObj)
			}

			planObjects := []runtime.Object{}
			for _, po := range tc.plan.Objects {
				obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(po)
				if err != nil {
					t.Errorf("failed to convert obj from plan into unstructured: %v; %v", po, err)
				}
				uObj := &unstructured.Unstructured{Object: obj}

				planObjects = append(planObjects, uObj)
			}
			plan.Objects = planObjects

			ctrl := gomock.NewController(t)
			mockLogSink := mocks.NewMockLogSink(ctrl)
			mockLogSink.EXPECT().Init(gomock.Any())
			mockLogSink.EXPECT().Enabled(gomock.Any()).Return(true).AnyTimes()

			logger := logr.New(mockLogSink)

			if _, err := desiredset.Diff(logger, plan, &v1alpha1.BundleDeployment{}, ns, objects...); err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if !plansMatch(plan.Update, tc.expectedPlan.Update) {
				t.Errorf("unexpected plan.Update: expected \n\t %v\n\t got \n\t%v", tc.expectedPlan.Update, plan.Update)
			}
		})
	}
}

func plansMatch(one, other map[schema.GroupVersionKind]map[objectset.ObjectKey]string) bool {
	if len(one) != len(other) {
		return false
	}

	for gvk, okm := range one {
		otherObjectKeyMap, ok := other[gvk]

		if !ok {
			return false
		}

		if !maps.Equal(okm, otherObjectKeyMap) {
			return false
		}
	}

	return true
}

//nolint:unparam
func deployment(ns, name string, replicas int, modifiers ...func(*appsv1.Deployment)) *appsv1.Deployment {
	depl := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(replicas)),
		},
	}

	for _, m := range modifiers {
		m(depl)
	}

	return depl
}

func statefulSet(ns, name string, replicas int) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(replicas)),
		},
	}
}

func hpav1(
	namespace, name string,
	min *int32,
	max int32,
	refAPIVersion, refKind, refName string,
) *autoscalingv1.HorizontalPodAutoscaler {
	return &autoscalingv1.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HorizontalPodAutoscaler",
			APIVersion: "autoscaling/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
				APIVersion: refAPIVersion,
				Kind:       refKind,
				Name:       refName,
			},
			MinReplicas: min,
			MaxReplicas: max,
			// No metrics, but should not matter
		},
	}
}

//nolint:unparam
func hpa(
	namespace, name string,
	min *int32,
	max int32,
	refAPIVersion, refKind, refName string,
) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HorizontalPodAutoscaler",
			APIVersion: "autoscaling/v2",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: refAPIVersion,
				Kind:       refKind,
				Name:       refName,
			},
			MinReplicas: min,
			MaxReplicas: max,
			// No metrics, but should not matter
		},
	}
}

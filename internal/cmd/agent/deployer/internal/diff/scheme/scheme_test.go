package scheme

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TestSchemeDefaultsDeployment verifies that the scheme sets critical defaults
// on Deployments. Without these defaults, drift detection produces false
// positives because the API server applies them on admission and the live
// object will always differ from the desired object.
//
// Regression guard for: https://github.com/rancher/fleet/issues/5020
func TestSchemeDefaultsDeployment(t *testing.T) {
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "app",
						Image: "nginx:1.25",
					}},
				},
			},
		},
	}

	Scheme.Default(dep)

	// Deployment-level defaults
	if dep.Spec.RevisionHistoryLimit == nil || *dep.Spec.RevisionHistoryLimit != 10 {
		t.Error("expected RevisionHistoryLimit to default to 10")
	}
	if dep.Spec.ProgressDeadlineSeconds == nil || *dep.Spec.ProgressDeadlineSeconds != 600 {
		t.Error("expected ProgressDeadlineSeconds to default to 600")
	}
	if dep.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Errorf("expected Strategy.Type RollingUpdate, got %q", dep.Spec.Strategy.Type)
	}

	// Pod-level defaults
	pod := &dep.Spec.Template.Spec
	if pod.RestartPolicy != corev1.RestartPolicyAlways {
		t.Errorf("expected RestartPolicy Always, got %q", pod.RestartPolicy)
	}
	if pod.DNSPolicy != corev1.DNSClusterFirst {
		t.Errorf("expected DNSPolicy ClusterFirst, got %q", pod.DNSPolicy)
	}

	// Container-level defaults
	c := &pod.Containers[0]
	if c.TerminationMessagePath != "/dev/termination-log" {
		t.Errorf("expected TerminationMessagePath /dev/termination-log, got %q", c.TerminationMessagePath)
	}
	if c.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
		t.Errorf("expected TerminationMessagePolicy File, got %q", c.TerminationMessagePolicy)
	}
	if c.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Errorf("expected ImagePullPolicy IfNotPresent, got %q", c.ImagePullPolicy)
	}
}

// TestSchemeDefaultsService verifies Service defaults.
func TestSchemeDefaultsService(t *testing.T) {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Port: 80,
			}},
		},
	}

	Scheme.Default(svc)

	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("expected Type ClusterIP, got %q", svc.Spec.Type)
	}
	if svc.Spec.SessionAffinity != corev1.ServiceAffinityNone {
		t.Errorf("expected SessionAffinity None, got %q", svc.Spec.SessionAffinity)
	}
	if svc.Spec.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("expected Port protocol TCP, got %q", svc.Spec.Ports[0].Protocol)
	}
}

// TestSchemeDefaultsJob verifies Job defaults.
func TestSchemeDefaultsJob(t *testing.T) {
	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "work",
						Image: "busybox",
					}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	Scheme.Default(job)

	if job.Spec.Completions == nil || *job.Spec.Completions != 1 {
		t.Error("expected Completions to default to 1")
	}
	if job.Spec.Parallelism == nil || *job.Spec.Parallelism != 1 {
		t.Error("expected Parallelism to default to 1")
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 6 {
		t.Error("expected BackoffLimit to default to 6")
	}
}

// TestSchemeDefaultsProduceNonEmptyPatch verifies that defaulting a minimal
// object actually changes it. This is the core invariant that drift detection
// relies on: without defaults, generateSchemeDefaultPatch returns "{}" and
// the diff engine compares un-defaulted desired state against server-defaulted
// live state, causing false drift on every field the API server defaulted.
func TestSchemeDefaultsProduceNonEmptyPatch(t *testing.T) {
	cases := []struct {
		name string
		obj  runtime.Object
	}{
		{
			name: "Deployment",
			obj: &appsv1.Deployment{
				TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}},
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "c", Image: "img"}},
						},
					},
				},
			},
		},
		{
			name: "Service",
			obj: &corev1.Service{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
				Spec:     corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}},
			},
		},
		{
			name: "Job",
			obj: &batchv1.Job{
				TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{{Name: "c", Image: "img"}},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.obj.DeepCopyObject()
			Scheme.Default(tc.obj)
			if reflect.DeepEqual(before, tc.obj) {
				t.Errorf("Scheme.Default() did not modify %s — defaulting is broken", tc.name)
			}
		})
	}
}

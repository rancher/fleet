package deployer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestSetNamespaceLabelsAndAnnotations(t *testing.T) {
	tests := map[string]struct {
		bd         *fleet.BundleDeployment
		ns         corev1.Namespace
		release    string
		expectedNs corev1.Namespace
	}{
		"Empty sets of NamespaceLabels and NamespaceAnnotations are supported": {
			bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
				Options: fleet.BundleDeploymentOptions{
					NamespaceLabels:      nil, // equivalent to map[string]string{}
					NamespaceAnnotations: nil,
				},
			}},
			ns: corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "namespace",
					Labels: map[string]string{"kubernetes.io/metadata.name": "namespace"},
				},
			},
			release: "namespace/foo/bar",
			expectedNs: corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "namespace",
					Labels:      map[string]string{"kubernetes.io/metadata.name": "namespace"},
					Annotations: nil,
				},
			},
		},

		"NamespaceLabels and NamespaceAnnotations are appended": {
			bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
				Options: fleet.BundleDeploymentOptions{
					NamespaceLabels:      map[string]string{"optLabel1": "optValue1", "optLabel2": "optValue2"},
					NamespaceAnnotations: map[string]string{"optAnn1": "optValue1"},
				},
			}},
			ns: corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "namespace",
					Labels: map[string]string{"kubernetes.io/metadata.name": "namespace"},
				},
			},
			release: "namespace/foo/bar",
			expectedNs: corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "namespace",
					Labels:      map[string]string{"kubernetes.io/metadata.name": "namespace", "optLabel1": "optValue1", "optLabel2": "optValue2"},
					Annotations: map[string]string{"optAnn1": "optValue1"},
				},
			},
		},

		"NamespaceLabels and NamespaceAnnotations removes entries that are not in the options, except the name label": {
			bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
				Options: fleet.BundleDeploymentOptions{
					NamespaceLabels:      map[string]string{"optLabel": "optValue"},
					NamespaceAnnotations: map[string]string{},
				},
			}},
			ns: corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "namespace",
					Labels:      map[string]string{"nsLabel": "nsValue", "kubernetes.io/metadata.name": "namespace"},
					Annotations: map[string]string{"nsAnn": "nsValue"},
				},
			},
			release: "namespace/foo/bar",
			expectedNs: corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "namespace",
					Labels:      map[string]string{"optLabel": "optValue", "kubernetes.io/metadata.name": "namespace"},
					Annotations: map[string]string{},
				},
			},
		},

		"NamespaceLabels and NamespaceAnnotations updates existing values": {
			bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
				Options: fleet.BundleDeploymentOptions{
					NamespaceLabels:      map[string]string{"bdLabel": "labelUpdated"},
					NamespaceAnnotations: map[string]string{"bdAnn": "annUpdated"},
				},
			}},
			ns: corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "namespace",
					Labels:      map[string]string{"bdLabel": "nsValue", "kubernetes.io/metadata.name": "namespace"},
					Annotations: map[string]string{"bdAnn": "nsValue"},
				},
			},
			release: "namespace/foo/bar",
			expectedNs: corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "namespace",
					Labels:      map[string]string{"bdLabel": "labelUpdated", "kubernetes.io/metadata.name": "namespace"},
					Annotations: map[string]string{"bdAnn": "annUpdated"},
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			utilruntime.Must(clientgoscheme.AddToScheme(scheme))
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&test.ns).Build()
			h := Deployer{
				client: client,
			}
			err := h.setNamespaceLabelsAndAnnotations(context.Background(), test.bd, test.release)
			if err != nil {
				t.Errorf("expected nil error: got %v", err)
			}

			ns := &corev1.Namespace{}
			err = client.Get(context.Background(), types.NamespacedName{Name: test.ns.Name}, ns)
			if err != nil {
				t.Errorf("expected nil error: got %v", err)
			}
			for k, v := range test.expectedNs.Labels {
				if ns.Labels[k] != v {
					t.Errorf("expected label %s: %s, got %s", k, v, ns.Labels[k])
				}
			}
			for k, v := range test.expectedNs.Annotations {
				if ns.Annotations[k] != v {
					t.Errorf("expected annotation %s: %s, got %s", k, v, ns.Annotations[k])
				}
			}
		})
	}
}

// TestSetNamespaceLabelsAndAnnotations_CreateNamespaceFalse verifies that
// disabling Helm namespace creation (CreateNamespace=false) does not prevent
// Fleet from applying namespaceLabels/namespaceAnnotations to the (already
// existing) namespace. CreateNamespace only governs creation; mutation is gated
// by the deployment's service account RBAC, not by this flag.
func TestSetNamespaceLabelsAndAnnotations_CreateNamespaceFalse(t *testing.T) {
	createNS := false
	bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
		Options: fleet.BundleDeploymentOptions{
			CreateNamespace:      &createNS,
			NamespaceLabels:      map[string]string{"label": "value"},
			NamespaceAnnotations: map[string]string{"ann": "value"},
		},
	}}
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "namespace",
			Labels: map[string]string{"kubernetes.io/metadata.name": "namespace"},
		},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	updateCalled := false
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&ns).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				updateCalled = true
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()

	h := Deployer{client: fakeClient}
	err := h.setNamespaceLabelsAndAnnotations(context.Background(), bd, "namespace/foo/bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !updateCalled {
		t.Error("namespace UPDATE was not attempted when CreateNamespace is false; mutation must not be gated by CreateNamespace")
	}

	result := &corev1.Namespace{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "namespace"}, result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Labels["label"] != "value" {
		t.Errorf("label: got %q, want %q", result.Labels["label"], "value")
	}
	if result.Annotations["ann"] != "value" {
		t.Errorf("annotation: got %q, want %q", result.Annotations["ann"], "value")
	}
}

// TestSetNamespaceLabelsAndAnnotations_ForbiddenSurfaces verifies that a
// permission error from the namespace client is wrapped such that it is still
// detectable as a Forbidden error (so the caller can record it as a status
// condition instead of requeuing forever).
func TestSetNamespaceLabelsAndAnnotations_ForbiddenSurfaces(t *testing.T) {
	bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
		Options: fleet.BundleDeploymentOptions{
			NamespaceLabels: map[string]string{"label": "value"},
		},
	}}
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "namespace",
			Labels: map[string]string{"kubernetes.io/metadata.name": "namespace"},
		},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "namespaces"}, "namespace", errors.New("nope"))
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&ns).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				return forbidden
			},
		}).
		Build()

	h := Deployer{client: fakeClient}
	err := h.setNamespaceLabelsAndAnnotations(context.Background(), bd, "namespace/foo/bar")
	if err == nil {
		t.Fatal("expected a forbidden error, got nil")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected error to be detectable as Forbidden, got %v", err)
	}

	if do, status := forbiddenToStatus(err, fleet.BundleDeploymentStatus{}); !do {
		t.Error("forbiddenToStatus did not record the forbidden error as a status condition")
	} else if status.Ready {
		t.Error("expected status.Ready to be false")
	}
}

// TestNamespaceForbiddenError verifies that the typed error DeployBundle
// returns for a denied namespace patch is both detectable via errors.As (so the
// controller can do a controlled requeue) and still unwraps to a Forbidden
// error.
func TestNamespaceForbiddenError(t *testing.T) {
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "namespaces"}, "namespace", errors.New("nope"))
	err := error(&NamespaceForbiddenError{err: forbidden})

	var nsErr *NamespaceForbiddenError
	if !errors.As(err, &nsErr) {
		t.Errorf("expected error to be detectable as *NamespaceForbiddenError, got %v", err)
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected error to unwrap to a Forbidden error, got %v", err)
	}
}

func TestSetNamespaceLabelsAndAnnotationsError(t *testing.T) {
	bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
		Options: fleet.BundleDeploymentOptions{
			NamespaceLabels:      map[string]string{"optLabel1": "optValue1", "optLabel2": "optValue2"},
			NamespaceAnnotations: map[string]string{"optAnn1": "optValue1"},
		},
	}}
	release := "test/foo/bar"

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := Deployer{
		client: client,
	}

	err := h.setNamespaceLabelsAndAnnotations(context.Background(), bd, release)

	if !apierrors.IsNotFound(err) {
		t.Errorf("expected not found error: got %v", err)
	}
}

// TestSetNamespaceLabelsAndAnnotations_NoUpdateWhenAlreadyCorrect verifies that
// updateNamespace is not called when the namespace already reflects the desired state.
// This guards against the broken reflect.DeepEqual check that compared raw option
// labels to ns.Labels; ns.Labels always includes kubernetes.io/metadata.name and
// may include preserved pod-security labels, so a direct equality check never holds.
func TestSetNamespaceLabelsAndAnnotations_NoUpdateWhenAlreadyCorrect(t *testing.T) {
	bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
		Options: fleet.BundleDeploymentOptions{
			NamespaceLabels:      map[string]string{"optLabel": "optValue"},
			NamespaceAnnotations: map[string]string{"optAnn": "optValue"},
		},
	}}
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "namespace",
			Labels:      map[string]string{"kubernetes.io/metadata.name": "namespace", "optLabel": "optValue"},
			Annotations: map[string]string{"optAnn": "optValue"},
		},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	updateCalled := false
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&ns).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				updateCalled = true
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()

	h := Deployer{client: fakeClient}
	err := h.setNamespaceLabelsAndAnnotations(context.Background(), bd, "namespace/foo/bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updateCalled {
		t.Error("updateNamespace was called when namespace was already in the desired state")
	}
}

func TestAddLabelsFromOptions_PodSecurityLabelsFiltered(t *testing.T) {
	tests := map[string]struct {
		nsLabels       map[string]string
		optLabels      map[string]string
		expectedLabels map[string]string
	}{
		"pod-security.kubernetes.io labels in optLabels are not applied to namespace": {
			nsLabels: map[string]string{"kubernetes.io/metadata.name": "ns"},
			optLabels: map[string]string{
				"pod-security.kubernetes.io/enforce": "privileged",
				"pod-security.kubernetes.io/audit":   "privileged",
				"pod-security.kubernetes.io/warn":    "privileged",
				"safe-label":                         "value",
			},
			expectedLabels: map[string]string{
				"kubernetes.io/metadata.name": "ns",
				"safe-label":                  "value",
			},
		},
		"existing pod-security.kubernetes.io labels on namespace are preserved": {
			nsLabels: map[string]string{
				"kubernetes.io/metadata.name":        "ns",
				"pod-security.kubernetes.io/enforce": "baseline",
				"pod-security.kubernetes.io/audit":   "baseline",
			},
			optLabels: map[string]string{
				"pod-security.kubernetes.io/enforce": "privileged",
				"app-label":                          "value",
			},
			expectedLabels: map[string]string{
				"kubernetes.io/metadata.name":        "ns",
				"pod-security.kubernetes.io/enforce": "baseline",
				"pod-security.kubernetes.io/audit":   "baseline",
				"app-label":                          "value",
			},
		},
		"non-security labels work normally": {
			nsLabels: map[string]string{
				"kubernetes.io/metadata.name": "ns",
				"old-label":                   "old-value",
			},
			optLabels: map[string]string{
				"new-label": "new-value",
			},
			expectedLabels: map[string]string{
				"kubernetes.io/metadata.name": "ns",
				"new-label":                   "new-value",
			},
		},
		"pod-security.kubernetes.io labels with custom suffixes are also filtered": {
			nsLabels: map[string]string{"kubernetes.io/metadata.name": "ns"},
			optLabels: map[string]string{
				"pod-security.kubernetes.io/enforce-version": "v1.25",
				"pod-security.kubernetes.io/audit-version":   "v1.25",
				"safe-label": "value",
			},
			expectedLabels: map[string]string{
				"kubernetes.io/metadata.name": "ns",
				"safe-label":                  "value",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			addLabelsFromOptions(logr.Discard(), test.nsLabels, test.optLabels)

			if len(test.nsLabels) != len(test.expectedLabels) {
				t.Errorf("expected %d labels, got %d: %v", len(test.expectedLabels), len(test.nsLabels), test.nsLabels)
			}
			for k, v := range test.expectedLabels {
				if test.nsLabels[k] != v {
					t.Errorf("expected label %s=%s, got %s", k, v, test.nsLabels[k])
				}
			}
		})
	}
}

func TestSetNamespaceLabelsAndAnnotations_PodSecurityLabelsPreserved(t *testing.T) {
	bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
		Options: fleet.BundleDeploymentOptions{
			NamespaceLabels: map[string]string{
				"pod-security.kubernetes.io/enforce": "privileged",
				"pod-security.kubernetes.io/audit":   "privileged",
				"pod-security.kubernetes.io/warn":    "privileged",
				"app-label":                          "value",
			},
		},
	}}
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace",
			Labels: map[string]string{
				"kubernetes.io/metadata.name":        "namespace",
				"pod-security.kubernetes.io/enforce": "restricted",
				"pod-security.kubernetes.io/audit":   "restricted",
			},
		},
	}
	release := "namespace/foo/bar"

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ns).Build()
	h := Deployer{client: client}

	err := h.setNamespaceLabelsAndAnnotations(context.Background(), bd, release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := &corev1.Namespace{}
	err = client.Get(context.Background(), types.NamespacedName{Name: "namespace"}, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Errorf("pod-security.kubernetes.io/enforce: got %s, want restricted", result.Labels["pod-security.kubernetes.io/enforce"])
	}
	if result.Labels["pod-security.kubernetes.io/audit"] != "restricted" {
		t.Errorf("pod-security.kubernetes.io/audit: got %s, want restricted", result.Labels["pod-security.kubernetes.io/audit"])
	}
	if result.Labels["app-label"] != "value" {
		t.Errorf("app-label: got %s, want value", result.Labels["app-label"])
	}
}

func TestIsStateAccepted(t *testing.T) {
	tests := []struct {
		name     string
		state    fleet.BundleState
		accepted []fleet.BundleState
		want     bool
	}{
		// Default behavior (nil or empty acceptedStates)
		{"default accepts Ready", fleet.Ready, nil, true},
		{"default rejects Modified", fleet.Modified, nil, false},
		{"default rejects NotReady", fleet.NotReady, nil, false},

		// Explicit acceptedStates
		{"accepts listed state", fleet.Modified, []fleet.BundleState{fleet.Ready, fleet.Modified}, true},
		{"rejects unlisted state", fleet.NotReady, []fleet.BundleState{fleet.Ready, fleet.Modified}, false},
		{"accepts single non-Ready state", fleet.WaitApplied, []fleet.BundleState{fleet.WaitApplied}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStateAccepted(tc.state, tc.accepted); got != tc.want {
				t.Errorf("isStateAccepted(%q, %v) = %v, want %v", tc.state, tc.accepted, got, tc.want)
			}
		})
	}
}

func TestDeployErrToStatus(t *testing.T) {
	tests := []struct {
		name      string
		errMsg    string
		wantMatch bool
	}{
		{"nil error", "", false},
		{"YAML parse error (Helm v3)", "YAML parse error on foo.yaml: yaml: line 1: did not find expected node content", true},
		{"MalformedYAMLError (Helm v4)", "MalformedYAMLError on foo.yaml: yaml: unmarshal errors", true},
		{"error validating data (client-side schema)", `error validating "": error validating data: ValidationError(Deployment.spec.template.spec.containers[0].lifecycle): unknown field "preStart" in io.k8s.api.core.v1.Lifecycle`, true},
		{"unknown field via SSA (API server strict validation)", `Deployment.apps "test" is invalid: spec.template.spec.containers[0].lifecycle.preStart: Invalid value: "null": unknown field`, true},
		{"unknown field via strict decoding", `strict decoding error: unknown field "spec.template.spec.containers[0].lifecycle.preStart"`, true},
		{"immutable spec", "Forbidden: spec is immutable after creation", true},
		{"forbidden update", "Forbidden: updates to statefulset spec for fields other than 'replicas' are forbidden", true},
		{"timed out", "timed out waiting for the condition", true},
		{"transient error (should not match)", "dial tcp: connection refused", false},
		{"not found (should not match)", "resource not found", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.errMsg != "" {
				err = fmt.Errorf("%s", tc.errMsg)
			}
			status := fleet.BundleDeploymentStatus{}
			got, _ := deployErrToStatus(err, status)
			if got != tc.wantMatch {
				t.Errorf("deployErrToStatus(%q) matched = %v, want %v", tc.errMsg, got, tc.wantMatch)
			}
		})
	}
}

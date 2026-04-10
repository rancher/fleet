package deployer

import (
	"context"
	"fmt"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

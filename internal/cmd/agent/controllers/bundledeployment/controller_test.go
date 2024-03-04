package bundledeployment

//go:generate mockgen --build_flags=--mod=mod -destination=../../../controller/mocks/dynamic_mock.go -package mocks k8s.io/client-go/dynamic Interface,NamespaceableResourceInterface

import (
	"testing"

	"github.com/golang/mock/gomock"

	"github.com/rancher/fleet/internal/cmd/controller/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestSetNamespaceLabelsAndAnnotations(t *testing.T) {
	tests := map[string]struct {
		bd         *fleet.BundleDeployment
		ns         corev1.Namespace
		release    string
		expectedNs corev1.Namespace
	}{
		"NamespaceLabels and NamespaceAnnotations are appended": {
			bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
				Options: fleet.BundleDeploymentOptions{
					NamespaceLabels:      &map[string]string{"optLabel1": "optValue1", "optLabel2": "optValue2"},
					NamespaceAnnotations: &map[string]string{"optAnn1": "optValue1"},
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
					NamespaceLabels:      &map[string]string{"optLabel": "optValue"},
					NamespaceAnnotations: &map[string]string{},
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
					NamespaceLabels:      &map[string]string{"bdLabel": "labelUpdated"},
					NamespaceAnnotations: &map[string]string{"bdAnn": "annUpdated"},
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
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockDynamic := mocks.NewMockInterface(ctrl)
			mockNamespaceableResourceInterface := mocks.NewMockNamespaceableResourceInterface(ctrl)
			u, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&test.ns)
			// Resource will be called twice, one time for UPDATE and another time for LIST
			mockDynamic.EXPECT().Resource(gomock.Any()).Return(mockNamespaceableResourceInterface).Times(2)
			mockNamespaceableResourceInterface.EXPECT().Get(gomock.Any(), "namespace", metav1.GetOptions{}).
				Return(&unstructured.Unstructured{Object: u}, nil).Times(1)
			uns, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&test.expectedNs)
			mockNamespaceableResourceInterface.EXPECT().Update(gomock.Any(), &unstructured.Unstructured{Object: uns}, gomock.Any()).Times(1)

			h := handler{
				dynamic: mockDynamic,
			}
			err := h.setNamespaceLabelsAndAnnotations(test.bd, test.release)

			if err != nil {
				t.Errorf("expected nil error: got %v", err)
			}
		})
	}
}

func TestSetNamespaceLabelsAndAnnotationsError(t *testing.T) {
	bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
		Options: fleet.BundleDeploymentOptions{
			NamespaceLabels:      &map[string]string{"optLabel1": "optValue1", "optLabel2": "optValue2"},
			NamespaceAnnotations: &map[string]string{"optAnn1": "optValue1"},
		},
	}}
	release := "test/foo/bar"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDynamic := mocks.NewMockInterface(ctrl)
	mockNamespaceableResourceInterface := mocks.NewMockNamespaceableResourceInterface(ctrl)
	mockDynamic.EXPECT().Resource(gomock.Any()).Return(mockNamespaceableResourceInterface).Times(1)
	mockNamespaceableResourceInterface.EXPECT().Get(gomock.Any(), "test", metav1.GetOptions{}).
		Return(nil, apierrors.NewNotFound(corev1.Resource("namespace"), "test")).Times(1)
	h := handler{
		dynamic: mockDynamic,
	}
	err := h.setNamespaceLabelsAndAnnotations(bd, release)

	if !apierrors.IsNotFound(err) {
		t.Errorf("expected not found error: got %v", err)
	}
}

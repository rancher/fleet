package monitor

import (
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func Test_calculateResourceCounts(t *testing.T) {
	type args struct {
		all      []runtime.Object
		nonReady []fleet.NonReadyStatus
		modified []fleet.ModifiedStatus
	}
	tests := []struct {
		name string
		args args
		want fleet.ResourceCounts
	}{
		{
			name: "all ready",
			args: args{
				all: []runtime.Object{
					&corev1.ConfigMap{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ConfigMap",
							APIVersion: "v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "testns",
							Name:      "testcm",
						},
					},
					&corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Secret",
							APIVersion: "v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "testns",
							Name:      "testsecret",
						},
					},
				},
			},
			want: fleet.ResourceCounts{DesiredReady: 2, Ready: 2},
		},
		{
			name: "orphaned",
			args: args{
				all: []runtime.Object{
					&corev1.ConfigMap{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ConfigMap",
							APIVersion: "v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "testns",
							Name:      "testcm",
						},
					},
				},
				modified: []fleet.ModifiedStatus{
					{
						Kind:       "Secret",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testsecret",
						Delete:     true,
					},
				},
			},
			want: fleet.ResourceCounts{DesiredReady: 1, Ready: 1, Orphaned: 1},
		},
		{
			name: "missing",
			args: args{
				all: []runtime.Object{
					&corev1.ConfigMap{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ConfigMap",
							APIVersion: "v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "testns",
							Name:      "testcm",
						},
					},
				},
				modified: []fleet.ModifiedStatus{
					{
						Kind:       "ConfigMap",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testcm",
						Create:     true,
					},
				},
			},
			want: fleet.ResourceCounts{DesiredReady: 1, Missing: 1},
		},
		{
			name: "modified",
			args: args{
				all: []runtime.Object{
					&corev1.ConfigMap{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ConfigMap",
							APIVersion: "v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "testns",
							Name:      "testcm",
						},
					},
				},
				modified: []fleet.ModifiedStatus{
					{
						Kind:       "ConfigMap",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testcm",
						Patch:      `{"data": {"foo": "bar"}`,
					},
				},
			},
			want: fleet.ResourceCounts{DesiredReady: 1, Modified: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calculateResourceCounts(tt.args.all, tt.args.nonReady, tt.args.modified); got != tt.want {
				t.Errorf("calculateResourceCounts() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

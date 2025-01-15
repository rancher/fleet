package monitor

import (
	"testing"

	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"
	"github.com/stretchr/testify/assert"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func Test_updateFromResources(t *testing.T) {
	type args struct {
		resources []fleet.BundleDeploymentResource
		nonReady  []fleet.NonReadyStatus
		modified  []fleet.ModifiedStatus
	}
	tests := []struct {
		name   string
		args   args
		assert func(*testing.T, fleet.BundleDeploymentStatus)
	}{
		{
			name: "all ready",
			args: args{
				resources: []fleet.BundleDeploymentResource{
					{
						Kind:       "ConfigMap",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testcm",
					},
					{
						Kind:       "Secret",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testsecret",
					},
				},
			},
			assert: func(t *testing.T, status fleet.BundleDeploymentStatus) {
				assert.Equal(t, status.ResourceCounts, fleet.ResourceCounts{DesiredReady: 2, Ready: 2})
				assert.Truef(t, status.Ready, "unexpected ready status")
				assert.Truef(t, status.NonModified, "unexpected non-modified status")
				assert.Lenf(t, status.Resources, 2, "unexpected resources length")
				assert.Emptyf(t, status.NonReadyStatus, "expected non-ready status to be empty")
				assert.Emptyf(t, status.ModifiedStatus, "expected modified status to be empty")
			},
		},
		{
			name: "orphaned",
			args: args{
				resources: []fleet.BundleDeploymentResource{
					{
						Kind:       "ConfigMap",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testcm",
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
			assert: func(t *testing.T, status fleet.BundleDeploymentStatus) {
				assert.Equal(t, status.ResourceCounts, fleet.ResourceCounts{DesiredReady: 1, Ready: 1, Orphaned: 1})
				assert.Truef(t, status.Ready, "unexpected ready status")
				assert.Falsef(t, status.NonModified, "unexpected non-modified status")
				assert.Lenf(t, status.Resources, 1, "unexpected resources length")
				assert.Len(t, status.ModifiedStatus, 1, "incorrect modified status length")
				assert.True(t, status.ModifiedStatus[0].Delete)
				assert.Emptyf(t, status.NonReadyStatus, "expected non-ready status to be empty")
			},
		},
		{
			name: "missing",
			args: args{
				resources: []fleet.BundleDeploymentResource{
					{
						Kind:       "ConfigMap",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testcm",
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
			assert: func(t *testing.T, status fleet.BundleDeploymentStatus) {
				assert.Equal(t, status.ResourceCounts, fleet.ResourceCounts{DesiredReady: 1, Missing: 1})
				assert.Truef(t, status.Ready, "unexpected ready status")
				assert.Falsef(t, status.NonModified, "unexpected non-modified status")
				assert.Lenf(t, status.Resources, 1, "unexpected resources length")
				assert.Len(t, status.ModifiedStatus, 1, "incorrect modified status length")
				assert.True(t, status.ModifiedStatus[0].Create)
				assert.Emptyf(t, status.NonReadyStatus, "expected non-ready status to be empty")
			},
		},
		{
			name: "modified",
			args: args{
				resources: []fleet.BundleDeploymentResource{
					{
						Kind:       "ConfigMap",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testcm",
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
			assert: func(t *testing.T, status fleet.BundleDeploymentStatus) {
				assert.Equal(t, status.ResourceCounts, fleet.ResourceCounts{DesiredReady: 1, Modified: 1})
				assert.Truef(t, status.Ready, "unexpected ready status")
				assert.Falsef(t, status.NonModified, "unexpected non-modified status")
				assert.Lenf(t, status.Resources, 1, "unexpected resources length")
				assert.Len(t, status.ModifiedStatus, 1, "incorrect modified status length")
				assert.NotEmpty(t, status.ModifiedStatus[0].Patch)
				assert.Emptyf(t, status.NonReadyStatus, "expected non-ready status to be empty")
			},
		},
		{
			name: "missing and non-ready",
			args: args{
				resources: []fleet.BundleDeploymentResource{
					{
						Kind:       "ConfigMap",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testcm",
					},
					{
						Kind:       "Pod",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testpod",
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
				nonReady: []fleet.NonReadyStatus{
					{
						Kind:       "Pod",
						APIVersion: "v1",
						Namespace:  "testns",
						Name:       "testpod",
						Summary: fleetv1.Summary{
							State:   "Evicted",
							Error:   true,
							Message: []string{"no space left on device"},
						},
					},
				},
			},
			assert: func(t *testing.T, status fleet.BundleDeploymentStatus) {
				assert.Equal(t, status.ResourceCounts, fleet.ResourceCounts{DesiredReady: 2, Modified: 1, NotReady: 1})
				assert.Falsef(t, status.Ready, "unexpected ready status")
				assert.Falsef(t, status.NonModified, "unexpected non-modified status")
				assert.Lenf(t, status.Resources, 2, "unexpected resources length")
				assert.Len(t, status.NonReadyStatus, 1, "incorrect non-ready status length")
				assert.NotEmptyf(t, status.NonReadyStatus[0].Summary, "unexpected empty summary for non-ready resource")
				assert.Len(t, status.ModifiedStatus, 1, "incorrect modified status length")
				assert.NotEmpty(t, status.ModifiedStatus[0].Patch)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var status fleet.BundleDeploymentStatus
			updateFromResources(&status, tt.args.resources, tt.args.nonReady, tt.args.modified)

			tt.assert(t, status)
		})
	}
}

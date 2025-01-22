package resourcestatus

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"
)

func TestSetResources(t *testing.T) {
	list := &fleet.BundleDeploymentList{
		Items: []fleet.BundleDeployment{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd1",
					Namespace: "ns1-cluster1-ns",
					Labels: map[string]string{
						fleet.ClusterLabel:          "cluster1",
						fleet.ClusterNamespaceLabel: "c-ns1",
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "id2",
				},
				Status: fleet.BundleDeploymentStatus{
					AppliedDeploymentID: "id1",
					Resources: []fleet.BundleDeploymentResource{
						{
							Kind:       "Deployment",
							APIVersion: "v1",
							Name:       "web",
							Namespace:  "default",
						},
						{
							// extra service for one cluster
							Kind:       "Service",
							APIVersion: "v1",
							Name:       "web-svc",
							Namespace:  "default",
						},
					},
					ResourceCounts: fleet.ResourceCounts{
						DesiredReady: 2,
						WaitApplied:  2,
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd1",
					Namespace: "ns1-cluster2-ns",
					Labels: map[string]string{
						fleet.ClusterLabel:          "cluster2",
						fleet.ClusterNamespaceLabel: "c-ns1",
					},
				},
				Status: fleet.BundleDeploymentStatus{
					Resources: []fleet.BundleDeploymentResource{
						{
							Kind:       "Deployment",
							APIVersion: "v1",
							Name:       "web",
							Namespace:  "default",
						},
					},
					ResourceCounts: fleet.ResourceCounts{
						DesiredReady: 1,
						Ready:        1,
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd2",
					Namespace: "ns1-cluster2-ns",
					Labels: map[string]string{
						fleet.ClusterLabel:          "cluster2",
						fleet.ClusterNamespaceLabel: "c-ns1",
					},
				},
				Status: fleet.BundleDeploymentStatus{
					Resources: []fleet.BundleDeploymentResource{
						{
							Kind:       "ConfigMap",
							APIVersion: "v1",
							Name:       "cm-web",
							Namespace:  "default",
						},
					},
					ResourceCounts: fleet.ResourceCounts{
						DesiredReady: 1,
						Ready:        1,
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd1",
					Namespace: "ns2-cluster1",
					Labels: map[string]string{
						fleet.ClusterLabel:          "cluster1",
						fleet.ClusterNamespaceLabel: "c-ns2",
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "id2",
				},
				Status: fleet.BundleDeploymentStatus{
					AppliedDeploymentID: "id1",
					NonReadyStatus: []fleet.NonReadyStatus{
						{
							Kind:       "Deployment",
							APIVersion: "v1",
							Name:       "web",
							Namespace:  "default",
							Summary: summary.Summary{
								State:         "Pending",
								Error:         true,
								Transitioning: true,
								Message:       []string{"message1", "message2"},
							},
						},
					},
					Resources: []fleet.BundleDeploymentResource{
						{
							Kind:       "Deployment",
							APIVersion: "v1",
							Name:       "web",
							Namespace:  "default",
						},
					},
					ResourceCounts: fleet.ResourceCounts{
						DesiredReady: 1,
						NotReady:     1,
					},
				},
			},
		},
	}

	var status fleet.GitRepoStatus
	SetResources(list, &status.StatusBase)

	assert.Len(t, status.Resources, 3)
	assert.Contains(t, status.Resources, fleet.Resource{
		APIVersion: "v1",
		Kind:       "Deployment",
		Type:       "deployment",
		ID:         "default/web",

		Namespace: "default",
		Name:      "web",

		IncompleteState: false,
		State:           "WaitApplied",
		Error:           false,
		Transitioning:   false,
		Message:         "",
		PerClusterState: []fleet.ResourcePerClusterState{
			{
				State:     "WaitApplied",
				ClusterID: "c-ns1/cluster1",
			},
			{
				State:     "NotReady",
				ClusterID: "c-ns1/cluster2",
			},
			{
				State:         "Pending",
				ClusterID:     "c-ns2/cluster1",
				Error:         true,
				Transitioning: true,
				Message:       "message1; message2",
				Patch:         nil,
			},
		},
	})
	assert.Contains(t, status.Resources, fleet.Resource{
		APIVersion: "v1",
		Kind:       "Service",
		Type:       "service",
		ID:         "default/web-svc",

		Namespace: "default",
		Name:      "web-svc",

		IncompleteState: false,
		State:           "WaitApplied",
		Error:           false,
		Transitioning:   false,
		Message:         "",
		PerClusterState: []fleet.ResourcePerClusterState{
			{
				State:     "WaitApplied",
				ClusterID: "c-ns1/cluster1",
			},
		},
	})

	assert.Empty(t, status.ResourceErrors)

	assert.Equal(t, fleet.ResourceCounts{
		Ready:        2,
		DesiredReady: 5,
		WaitApplied:  2,
		NotReady:     1,
	}, status.ResourceCounts)

	assert.Equal(t, map[string]*fleet.ResourceCounts{
		"c-ns1/cluster1": {
			DesiredReady: 2,
			WaitApplied:  2,
		},
		"c-ns1/cluster2": {
			DesiredReady: 2,
			Ready:        2,
		},
		"c-ns2/cluster1": {
			DesiredReady: 1,
			NotReady:     1,
		},
	}, status.PerClusterResourceCounts)

}

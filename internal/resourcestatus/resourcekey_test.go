package resourcestatus

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"
)

func TestSetResources(t *testing.T) {
	gitrepo := &fleet.GitRepo{
		Status: fleet.GitRepoStatus{
			StatusBase: fleet.StatusBase{
				Summary: fleet.BundleSummary{
					Ready:       2,
					WaitApplied: 1,
				},
			},
		},
	}
	list := &fleet.BundleDeploymentList{
		Items: []fleet.BundleDeployment{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "bd1",
					Labels: map[string]string{
						fleet.RepoLabel:             "gitrepo1",
						fleet.ClusterLabel:          "cluster1",
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
						{
							// extra service for one cluster
							Kind:       "Service",
							APIVersion: "v1",
							Name:       "web-svc",
							Namespace:  "default",
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "bd1",
					Labels: map[string]string{
						fleet.RepoLabel:             "gitrepo1",
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
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "bd1",
					Labels: map[string]string{
						fleet.RepoLabel:             "gitrepo1",
						fleet.ClusterLabel:          "cluster1",
						fleet.ClusterNamespaceLabel: "c-ns2",
					},
				},
				Status: fleet.BundleDeploymentStatus{
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
				},
			},
		},
	}

	SetResources(list, &gitrepo.Status.StatusBase)

	assert.Len(t, gitrepo.Status.Resources, 2)
	assert.Contains(t, gitrepo.Status.Resources, fleet.Resource{
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
				State:         "Pending",
				ClusterID:     "c-ns2/cluster1",
				Error:         true,
				Transitioning: true,
				Message:       "message1; message2",
				Patch:         nil,
			},
		},
	})
	assert.Contains(t, gitrepo.Status.Resources, fleet.Resource{
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
		PerClusterState: []fleet.ResourcePerClusterState{},
	})

	assert.Empty(t, gitrepo.Status.ResourceErrors)

	assert.Equal(t, gitrepo.Status.ResourceCounts.Ready, 0)
	assert.Equal(t, gitrepo.Status.ResourceCounts.DesiredReady, 4)
	assert.Equal(t, gitrepo.Status.ResourceCounts.WaitApplied, 3)
	assert.Equal(t, gitrepo.Status.ResourceCounts.Modified, 0)
	assert.Equal(t, gitrepo.Status.ResourceCounts.Orphaned, 0)
	assert.Equal(t, gitrepo.Status.ResourceCounts.Missing, 0)
	assert.Equal(t, gitrepo.Status.ResourceCounts.Unknown, 0)
	assert.Equal(t, gitrepo.Status.ResourceCounts.NotReady, 1)
}

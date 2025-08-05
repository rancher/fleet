package resourcestatus

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"
)

func TestSetResources(t *testing.T) {
	list := []fleet.BundleDeployment{
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
				Ready:               false,
				NonModified:         true,
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
				Ready:       true,
				NonModified: true,
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
				Ready:       true,
				NonModified: true,
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
				Ready:               false,
				NonModified:         true,
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
		PerClusterState: fleet.PerClusterState{
			Ready:       []string{"c-ns1/cluster2"},
			WaitApplied: []string{"c-ns1/cluster1"},
			Pending:     []string{"c-ns2/cluster1"},
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
		PerClusterState: fleet.PerClusterState{
			WaitApplied: []string{"c-ns1/cluster1"},
		},
	})

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

func TestPerClusterState(t *testing.T) {
	bundleDeploymentWithState := func(state string) fleet.BundleDeployment {
		return fleet.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bd1",
				Namespace: "ns1-cluster1",
				Labels: map[string]string{
					fleet.ClusterLabel:          "cluster",
					fleet.ClusterNamespaceLabel: "namespace",
				},
			},
			Spec: fleet.BundleDeploymentSpec{
				DeploymentID: "bd1",
			},
			Status: fleet.BundleDeploymentStatus{
				AppliedDeploymentID: "bd1",
				NonReadyStatus: []fleet.NonReadyStatus{
					{
						Kind:       "Deployment",
						APIVersion: "v1",
						Namespace:  "default",
						Name:       "web",
						Summary: summary.Summary{
							State: state,
						},
					},
				},
			},
		}
	}
	tests := []struct {
		name              string
		bundleDeployments []fleet.BundleDeployment
		expectedStatus    fleet.StatusBase
	}{
		{
			name:              "if the state of the NonReadyStatus resource is updating, then it should report it as NotReady",
			bundleDeployments: []fleet.BundleDeployment{bundleDeploymentWithState("updating")},
			expectedStatus: fleet.StatusBase{
				Resources: []fleet.Resource{
					{
						Namespace: "default",
						Name:      "web",
						PerClusterState: fleet.PerClusterState{
							NotReady: []string{"namespace/cluster"},
						},
					},
				},
			},
		},
		{
			name:              "if the state of the NonReadyStatus resource is unknown, then it should ignore the state",
			bundleDeployments: []fleet.BundleDeployment{bundleDeploymentWithState("")},
			expectedStatus: fleet.StatusBase{
				Resources: []fleet.Resource{
					{
						Namespace:       "default",
						Name:            "web",
						PerClusterState: fleet.PerClusterState{},
					},
				},
			},
		},
		{
			name:              "if the state of the NonReadyStatus resource is NotReady, then it should report it as NotReady",
			bundleDeployments: []fleet.BundleDeployment{bundleDeploymentWithState("NotReady")},
			expectedStatus: fleet.StatusBase{
				Resources: []fleet.Resource{
					{
						Namespace: "default",
						Name:      "web",
						PerClusterState: fleet.PerClusterState{
							NotReady: []string{"namespace/cluster"},
						},
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var status fleet.GitRepoStatus
			SetResources(test.bundleDeployments, &status.StatusBase)

			assert.Equal(t, test.expectedStatus.Resources[0].PerClusterState, status.StatusBase.Resources[0].PerClusterState,
				"Expected resources to match for bundle deployments: %v", test.bundleDeployments,
			)
		})
	}
}

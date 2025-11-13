package resourcestatus

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

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
			name:              "if the state of the resource is error, then it should report it as NotReady",
			bundleDeployments: []fleet.BundleDeployment{bundleDeploymentWithState("error")},
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
			name:              "if the state of the resource is updating, then it should report it as NotReady",
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
			name:              "if the state of the resource is unknown, then it should ignore the state",
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
			name:              "if the state of the resource is NotReady, then it should report it as NotReady",
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

func TestPerClusterStateTruncation(t *testing.T) {
	percluster := func(b, c int) fleet.BundleDeployment {
		workload := fmt.Sprintf("workload%02d", b)
		bd := fleet.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("bundlename%d", b),
				Namespace: fmt.Sprintf("ns-cluster%d", c),
				Labels: map[string]string{
					fleet.ClusterLabel:          fmt.Sprintf("d0-k3k-downstream%04d-downstream%04d", c, c),
					fleet.ClusterNamespaceLabel: "fleet-default",
				},
			},
			Spec: fleet.BundleDeploymentSpec{
				DeploymentID: "fakeid",
			},
			Status: fleet.BundleDeploymentStatus{
				AppliedDeploymentID: "fakeid",
				Resources: []fleet.BundleDeploymentResource{
					{Kind: "ConfigMap", APIVersion: "v1", Namespace: workload, Name: "cm-web"},
					{Kind: "Deployment", APIVersion: "v1", Namespace: workload, Name: "web"},
					{Kind: "Service", APIVersion: "v1", Namespace: workload, Name: "web-svc"},
				},
				ModifiedStatus: []fleet.ModifiedStatus{
					{Kind: "Secret", APIVersion: "v1", Namespace: workload, Name: "cm-creds", Create: true},
				},
				NonReadyStatus: []fleet.NonReadyStatus{
					{Kind: "Deployment", APIVersion: "v1", Namespace: workload, Name: "db", Summary: summary.Summary{State: "NotReady"}},
				},
			},
		}
		return bd
	}
	// we are not comparing the whole struct
	sizeOf := func(res []fleet.Resource) int {
		size := 0
		for _, r := range res {
			for _, s := range r.PerClusterState.Ready {
				size = size + len(s)
			}
			for _, s := range r.PerClusterState.NotReady {
				size = size + len(s)
			}
			for _, s := range r.PerClusterState.Missing {
				size = size + len(s)
			}
		}
		return size
	}

	n := 0
	maxBundle := 50
	maxCluster := 800
	var items = make([]fleet.BundleDeployment, maxBundle*maxCluster)
	for c := range maxCluster {
		for b := range maxBundle {
			items[n] = percluster(b, c)
			n = n + 1
		}
	}

	// different order should produce the same truncation
	ritems := slices.Clone(items)
	slices.Reverse(ritems)

	var status fleet.GitRepoStatus
	SetResources(items, &status.StatusBase)

	assert.Less(t, sizeOf(status.Resources), 1024*1024, "resources should be truncated to be less than 1MB")

	js, err := json.Marshal(status.Resources)
	require.NoError(t, err)

	// and the truncation is stable
	SetResources(items, &status.StatusBase)
	js2, err := json.Marshal(status.Resources)
	require.NoError(t, err)
	// avoid the long diff from assert.Equal
	assert.Equal(t, string(js), string(js2), "truncation should produce stable json for the same input")

	SetResources(ritems, &status.StatusBase)
	js2, err = json.Marshal(status.Resources)
	require.NoError(t, err)
	assert.Equal(t, string(js), string(js2), "truncation should produce stable json, when items are in a different order")
}

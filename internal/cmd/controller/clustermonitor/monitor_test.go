package clustermonitor_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/cmd/controller/clustermonitor"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
)

const threshold = 1 * time.Second

// BDStatusMatcher implements a gomock matcher for bundle deployment status.
// The matcher checks for empty modified and non-ready status, as well as offline state through Ready and Monitored
// conditions.
type BDStatusMatcher struct {
}

func (m BDStatusMatcher) Matches(x interface{}) bool {
	bd, ok := x.(*v1alpha1.BundleDeployment)
	if !ok || bd == nil {
		return false
	}

	bdStatus := bd.Status

	if bdStatus.ModifiedStatus != nil || bdStatus.NonReadyStatus != nil {
		return false
	}

	foundReady, foundMonitored := false, false
	for _, cond := range bdStatus.Conditions {
		if cond.Type == "Ready" {
			foundReady = true

			if !strings.Contains(cond.Message, "offline") {
				return false
			}
		} else if cond.Type == "Monitored" {
			foundMonitored = true

			if !strings.Contains(cond.Message, "offline") {
				return false
			}
		}

	}

	return foundReady && foundMonitored
}

func (m BDStatusMatcher) String() string {
	return "Bundle deployment status for offline cluster"
}

type clusterWithOfflineMarker struct {
	cluster   v1alpha1.Cluster
	isOffline bool
}

func Test_Run(t *testing.T) { //nolint: funlen // this is a test function, its length should not be an issue
	cases := []struct {
		name              string
		clusters          []clusterWithOfflineMarker
		listClustersErr   error
		bundleDeployments map[string][]v1alpha1.BundleDeployment // indexed by cluster namespace
		listBDErr         error
	}{
		{
			name:              "no cluster",
			clusters:          nil,
			listClustersErr:   nil,
			bundleDeployments: nil,
			listBDErr:         nil,
		},
		{
			name: "no offline cluster",
			clusters: []clusterWithOfflineMarker{
				{
					cluster: v1alpha1.Cluster{
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC()},
							},
						},
					},
				},
				{
					cluster: v1alpha1.Cluster{
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC()},
							},
						},
					},
				},
				{
					cluster: v1alpha1.Cluster{
						Status: v1alpha1.ClusterStatus{
							// eg. not yet registered downstream cluster
							Agent: v1alpha1.AgentStatus{ /* LastSeen is zero */ },
						},
					},
				},
			},
			listClustersErr:   nil,
			bundleDeployments: nil,
			listBDErr:         nil,
		},
		{
			name: "one offline cluster",
			clusters: []clusterWithOfflineMarker{
				{
					cluster: v1alpha1.Cluster{
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC()},
							},
						},
					},
				},
				{
					cluster: v1alpha1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name: "mycluster",
						},
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC().Add(-10 * threshold)},
							},
							Namespace: "clusterns1",
						},
					},
					isOffline: true,
				},
			},
			listClustersErr: nil,
			bundleDeployments: map[string][]v1alpha1.BundleDeployment{
				"clusterns1": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "mybundledeployment",
							Namespace: "clusterns1",
						},
						Status: v1alpha1.BundleDeploymentStatus{
							Conditions: []genericcondition.GenericCondition{
								{
									Type: "Ready",
								},
								{
									Type: "Monitored",
								},
							},
						},
					},
				},
			},
			listBDErr: nil,
		},
		{
			name: "multiple offline clusters",
			clusters: []clusterWithOfflineMarker{
				{
					cluster: v1alpha1.Cluster{
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC()},
							},
						},
					},
				},
				{
					cluster: v1alpha1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name: "mycluster",
						},
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC().Add(-10 * threshold)},
							},
							Namespace: "clusterns1",
						},
					},
					isOffline: true,
				},
				{
					cluster: v1alpha1.Cluster{
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC()},
							},
						},
					},
				},
				{
					cluster: v1alpha1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name: "mycluster2",
						},
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC().Add(-5 * threshold)},
							},
							Namespace: "clusterns2",
						},
					},
					isOffline: true,
				},
				{
					cluster: v1alpha1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Name: "mycluster3",
						},
						Status: v1alpha1.ClusterStatus{
							Agent: v1alpha1.AgentStatus{
								LastSeen: metav1.Time{Time: time.Now().UTC().Add(-3 * threshold)},
							},
							Namespace: "clusterns3",
						},
					},
					isOffline: true,
				},
			},
			listClustersErr: nil,
			bundleDeployments: map[string][]v1alpha1.BundleDeployment{
				"clusterns1": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "mybundledeployment",
							Namespace: "clusterns1",
						},
						Status: v1alpha1.BundleDeploymentStatus{
							Conditions: []genericcondition.GenericCondition{
								{
									Type: "Ready",
								},
								{
									Type: "Monitored",
								},
							},
						},
					},
				},
				"clusterns2": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "mybundledeployment",
							Namespace: "clusterns2",
						},
						Status: v1alpha1.BundleDeploymentStatus{
							Conditions: []genericcondition.GenericCondition{
								{
									Type: "Ready",
								},
								{
									Type: "Monitored",
								},
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "my-other-bundledeployment",
							Namespace: "clusterns2",
						},
						Status: v1alpha1.BundleDeploymentStatus{
							Conditions: []genericcondition.GenericCondition{
								{
									Type: "Ready",
								},
								{
									Type: "Monitored",
								},
							},
						},
					},
				},
				"clusterns3": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "mybundledeployment",
							Namespace: "clusterns3",
						},
						Status: v1alpha1.BundleDeploymentStatus{
							Conditions: []genericcondition.GenericCondition{
								{
									Type: "Ready",
								},
								{
									Type: "Monitored",
								},
							},
						},
					},
				},
			},
			listBDErr: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			testClient := mocks.NewMockK8sClient(ctrl)
			ctx, cancel := context.WithCancel(context.Background())

			defer cancel()

			testClient.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, list *v1alpha1.ClusterList, opts ...client.ListOption) error {
					for _, cl := range tc.clusters {
						list.Items = append(list.Items, cl.cluster)
					}

					return tc.listClustersErr
				},
			)

			for _, cl := range tc.clusters {
				if !cl.isOffline {
					continue
				}

				bundleDeplsForCluster := tc.bundleDeployments[cl.cluster.Status.Namespace]
				testClient.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, list *v1alpha1.BundleDeploymentList, opts ...client.ListOption) error {
						list.Items = append(list.Items, bundleDeplsForCluster...)

						return tc.listBDErr
					},
				)

				for _, bd := range bundleDeplsForCluster {
					testClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
						func(
							ctx context.Context,
							nsn types.NamespacedName,
							// b's initial value is never used, but the variable needs to be
							// named for its value to be overwritten with values we care
							// about, to simulate a response from the API server.
							b *v1alpha1.BundleDeployment, //nolint: staticcheck
							opts ...client.GetOption,
						) error {
							b = &bd //nolint: ineffassign,staticcheck // the value is used by the implementation, not directly by the tests.

							return nil
						},
					)

					srw := mocks.NewMockStatusWriter(ctrl)
					testClient.EXPECT().Status().Return(srw)

					srw.EXPECT().Update(ctx, &BDStatusMatcher{}).Return(nil)
				}
			}

			clustermonitor.UpdateOfflineBundleDeployments(ctx, testClient, threshold)
		})
	}
}

package clustermonitor_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/cmd/controller/clustermonitor"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
)

const threshold = 1 * time.Second

type clusterWithOfflineMarker struct {
	cluster                v1alpha1.Cluster
	isOffline              bool
	isAlreadyMarkedOffline bool
}

func Test_Run(t *testing.T) {
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
		{
			name: "multiple offline clusters, some of them already marked offline",
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
					isOffline:              true,
					isAlreadyMarkedOffline: true,
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
									Type:    "Ready",
									Message: "cluster is offline",
								},
								{
									Type:    "Monitored",
									Message: "cluster is offline",
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
									Type:    "Ready",
									Message: "cluster is offline",
								},
								{
									Type:    "Monitored",
									Message: "cluster is offline",
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
			ctx := t.Context()

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

				if cl.isAlreadyMarkedOffline {
					// skip status updates for bundle deployments which have already been
					// marked offline by a previous monitoring loop.
					continue
				}

				for _, bd := range bundleDeplsForCluster {
					testClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
						func(
							ctx context.Context,
							nsn types.NamespacedName,
							// b's initial value is never used, but the variable needs to be
							// named for its value to be overwritten with values we care
							// about, to simulate a response from the API server.
							b *v1alpha1.BundleDeployment,
							opts ...client.GetOption,
						) error {
							*b = bd

							return nil
						},
					)

					srw := mocks.NewMockStatusWriter(ctrl)
					testClient.EXPECT().Status().Return(srw)

					srw.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any()).Do(
						func(ctx context.Context, bd *v1alpha1.BundleDeployment, p client.Patch, opts ...any) {
							var foundReady, foundMonitored bool
							for _, c := range bd.Status.Conditions {
								switch c.Type {
								case "Ready":
									foundReady = true
									if c.Status != corev1.ConditionStatus("Unknown") {
										t.Errorf("expecting unknown ready condition status, got %s", c.Status)
									}
									if !strings.Contains(c.Message, "offline") {
										t.Errorf("expecting ready condition message to reflect offline state, got %s", c.Message)
									}
								case "Monitored":
									foundMonitored = true
									if !strings.Contains(c.Message, "offline") {
										t.Errorf("expecting ready condition message to reflect offline state, got %s", c.Message)
									}
								}
							}

							if !foundReady {
								t.Errorf("ready condition not found in BD status %v", bd.Status)
							}

							if !foundMonitored {
								t.Errorf("monitored condition not found in BD status %v", bd.Status)
							}
						}).Times(1)
				}
			}

			clustermonitor.UpdateOfflineBundleDeployments(ctx, testClient, threshold)
		})
	}
}

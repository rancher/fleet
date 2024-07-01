package clusterstatus

import (
	"context"
	"encoding/json"
	"time"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("ClusterStatus Ticker", func() {
	var (
		clusterClient        *fake.MockClientInterface[*fleet.Cluster, *fleet.ClusterList]
		nodeClient           *fake.MockNonNamespacedClientInterface[*v1.Node, *v1.NodeList]
		ctx                  context.Context
		cancel               context.CancelFunc
		agentNamespace       string
		clusterName          string
		clusterNamespace     string
		baseTime             metav1.Time
		lastTime             metav1.Time
		agentStatusNamespace string
	)

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		clusterClient = fake.NewMockClientInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
		nodeClient = fake.NewMockNonNamespacedClientInterface[*v1.Node, *v1.NodeList](ctrl)
		agentNamespace = "cattle-fleet-system"
		clusterName = "cluster-name"
		clusterNamespace = "cluster-namespace"
		baseTime = metav1.Now()
		ctx, cancel = context.WithCancel(context.TODO())
		clusterClient.EXPECT().Patch(clusterNamespace, clusterName, types.MergePatchType, gomock.Any(), "status").
			DoAndReturn(func(namespace, name string, pt types.PatchType, data []byte, subresources ...string) (fleet.Cluster, error) {
				cluster := &fleet.Cluster{}
				err := json.Unmarshal(data, cluster)
				if err != nil {
					return fleet.Cluster{}, err
				}
				// only storing the lastseen time value here.
				// We're not checking for values here because calling Expect,
				// for example, makes the mock call panic when it doesn't succeed
				lastTime = cluster.Status.Agent.LastSeen
				agentStatusNamespace = cluster.Status.Agent.Namespace
				return *cluster, nil
			}).AnyTimes()
		Ticker(ctx, agentNamespace, clusterNamespace, clusterName, time.Second*1, nodeClient, clusterClient)
	})

	It("Increases the timestamp used to call Patch", func() {
		By("Comparing every 2 seconds for a 6 seconds period")
		Consistently(func() bool {
			// return true when we're calling before Patch was even called
			if lastTime.IsZero() {
				return true
			}
			// check that the timestamp increases and the namespace is the expected one
			result := baseTime.Before(&lastTime) && agentStatusNamespace == agentNamespace
			baseTime = lastTime
			return result
		}, 6*time.Second, 2*time.Second).Should(BeTrue())
		// ensure that lastTime was set (which means Patch was successfully called)
		Expect(lastTime).ShouldNot(BeZero())
	})

	AfterEach(func() {
		cancel()
	})
})

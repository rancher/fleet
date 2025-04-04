package clusterstatus

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var clientBuilder = fake.NewClientBuilder()

var _ = Describe("ClusterStatus Ticker", func() {
	var (
		scheme *runtime.Scheme
		cancel context.CancelFunc

		ctx              context.Context
		clt              client.Client
		agentNamespace   string
		clusterName      string
		clusterNamespace string
		checkinInterval  time.Duration
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		utilruntime.Must(fleet.AddToScheme(scheme))

		agentNamespace = "cattle-fleet-system"
		clusterName = "cluster-name"
		clusterNamespace = "cluster-namespace"
		checkinInterval = time.Millisecond * 1

		interceptorFuncs := interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				defer cancel()

				bytes, err := patch.Data(obj)
				if err != nil {
					return err
				}

				clusterStatus := struct {
					Status struct {
						Agent fleet.AgentStatus `json:"agent"`
					} `json:"status"`
				}{}
				if err = json.Unmarshal(bytes, &clusterStatus); err != nil {
					return err
				}

				Expect(clusterStatus.Status.Agent.LastSeen.Time).To(BeTemporally("~", time.Now(), time.Minute*5),
					"time stamp should have been updated within the last 5 minutes")
				return nil
			},
		}

		ctx = context.Background()
		cluster := &fleet.Cluster{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Cluster",
				APIVersion: "fleet.cattle.io/v1alpha1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Status: fleet.ClusterStatus{
				Agent: fleet.AgentStatus{
					LastSeen: metav1.Time{Time: time.Now().Add(-time.Hour * 24)},
				},
			},
		}
		clt = clientBuilder.
			WithScheme(scheme).
			WithObjects(cluster).
			WithStatusSubresource(cluster).
			WithInterceptorFuncs(interceptorFuncs).
			Build()

		// Make sure the test is eventually aborted if the context is not canceled on success.
		ctx, cancel = context.WithDeadline(ctx, time.Now().Add(time.Second*30))
	})

	It("should patch the cluster status after checkinInterval", func() {
		Ticker(ctx, clt, agentNamespace, clusterNamespace, clusterName, checkinInterval)
		<-ctx.Done()
	})
})

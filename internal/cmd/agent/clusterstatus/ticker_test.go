package clusterstatus

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
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
		clt = fake.NewClientBuilder().
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

	It("should stop periodic patches when context is cancelled", func() {
		// Verifies the jitter select respects ctx.Done so the goroutine
		// exits cleanly instead of blocking forever on time.After.
		checkinInterval = time.Millisecond * 50

		var patchCount atomic.Int32
		interceptorFuncs := interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patchCount.Add(1)
				return nil
			},
		}
		clt = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&fleet.Cluster{
				TypeMeta:   metav1.TypeMeta{Kind: "Cluster", APIVersion: "fleet.cattle.io/v1alpha1"},
				ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: clusterNamespace},
			}).
			WithStatusSubresource(&fleet.Cluster{}).
			WithInterceptorFuncs(interceptorFuncs).
			Build()

		Ticker(ctx, clt, agentNamespace, clusterNamespace, clusterName, checkinInterval)

		// Wait for at least one periodic patch to confirm the ticker is running.
		Eventually(func() int32 { return patchCount.Load() }, time.Second).
			Should(BeNumerically(">=", 1))

		cancel()
		countAtCancel := patchCount.Load()

		// After cancellation the goroutine must exit; no additional patches.
		Consistently(func() int32 { return patchCount.Load() }, checkinInterval*3, checkinInterval).
			Should(Equal(countAtCancel))
	})

	It("should spread first periodic patch across the interval to avoid thundering herd", func() {
		// Start several concurrent agents and collect the timestamp of each
		// agent's first periodic patch. Jitter must distribute them across the
		// checkinInterval window rather than bunching them all at t=0.
		const agentCount = 5
		checkinInterval = time.Millisecond * 200

		var mu sync.Mutex
		firstPatchTimes := make([]time.Time, 0, agentCount)
		allDone := make(chan struct{})

		buildClient := func(name string) client.Client {
			interceptorFuncs := interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					mu.Lock()
					defer mu.Unlock()
					if len(firstPatchTimes) < agentCount {
						firstPatchTimes = append(firstPatchTimes, time.Now())
						if len(firstPatchTimes) == agentCount {
							close(allDone)
						}
					}
					return nil
				},
			}
			return fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(&fleet.Cluster{
					TypeMeta:   metav1.TypeMeta{Kind: "Cluster", APIVersion: "fleet.cattle.io/v1alpha1"},
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: clusterNamespace},
				}).
				WithStatusSubresource(&fleet.Cluster{}).
				WithInterceptorFuncs(interceptorFuncs).
				Build()
		}

		for i := range agentCount {
			name := clusterName + "-" + string(rune('a'+i))
			Ticker(ctx, buildClient(name), agentNamespace, clusterNamespace, name, checkinInterval)
		}

		select {
		case <-allDone:
		case <-time.After(checkinInterval * time.Duration(agentCount) * 3):
			Fail("timed out waiting for all agents to check in")
		}
		cancel()

		mu.Lock()
		defer mu.Unlock()

		// All agents started at the same moment; with jitter their first periodic
		// patches must not all arrive within a single millisecond of each other.
		earliest := firstPatchTimes[0]
		latest := firstPatchTimes[0]
		for _, t := range firstPatchTimes[1:] {
			if t.Before(earliest) {
				earliest = t
			}
			if t.After(latest) {
				latest = t
			}
		}
		spread := latest.Sub(earliest)
		Expect(spread).To(BeNumerically(">", time.Millisecond),
			"jitter should spread %d agents' first check-in across time, not bunch them at t=0", agentCount)
	})
})

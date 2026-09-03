package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/helmops/reconciler"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
)

// isHelmOp matches HelmOp reads, for the test clients in integrationtests/utils.
func isHelmOp(obj client.Object) bool {
	_, ok := obj.(*fleet.HelmOp)
	return ok
}

// copyHelmOpInto returns a CopyInto function serving the given stale HelmOp.
func copyHelmOpInto(stale *fleet.HelmOp) func(client.Object) bool {
	return func(into client.Object) bool {
		helmop, ok := into.(*fleet.HelmOp)
		if !ok {
			return false
		}
		stale.DeepCopyInto(helmop)

		return true
	}
}

// newChartRepo serves the package's helm repo index for the duration of a spec.
func newChartRepo() string {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, helmRepoIndex)
	}))
	DeferCleanup(svr.Close)

	return svr.URL
}

// newHelmOp returns a HelmOp carrying a shard-ref label, which keeps the
// manager-run reconcilers (using the default, unsharded ShardID) away from it so
// that the only writers are the ones a test drives explicitly.
func newHelmOp(name types.NamespacedName, shard, chart string) *fleet.HelmOp {
	return &fleet.HelmOp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
			Labels:    map[string]string{sharding.ShardingRefLabel: shard},
		},
		Spec: fleet.HelmOpSpec{
			BundleSpec: fleet.BundleSpec{
				BundleDeploymentOptions: fleet.BundleDeploymentOptions{
					Helm: &fleet.HelmOptions{
						Chart: chart,
					},
				},
			},
		},
	}
}

var _ = Describe("HelmOp status conflicts", func() {
	var helmOpName types.NamespacedName

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})

		helmOpName = types.NamespacedName{Name: "conflict-helmop", Namespace: namespace}
		Expect(k8sClient.Create(
			ctx,
			newHelmOp(helmOpName, "conflict-test-shard", "http://foo.bar/baz/test.tgz"),
		)).To(Succeed())
	})

	When("another controller writes a condition between the status reconciler's read and its patch", func() {
		It("does not drop that condition", func() {
			// Simulates the HelmOp reconciler publishing Accepted=True while the
			// status reconciler is mid-flight, holding a copy that predates it.
			injector := &utils.ConflictInjector{
				Client: k8sClient,
				Target: helmOpName,
				Match:  isHelmOp,
			}
			injector.Inject = func() {
				defer GinkgoRecover()
				Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
					live := &fleet.HelmOp{}
					if err := k8sClient.Get(ctx, helmOpName, live); err != nil {
						return err
					}
					live.Status.Conditions = append(live.Status.Conditions, genericcondition.GenericCondition{
						Type:    fleet.HelmOpAcceptedCondition,
						Status:  corev1.ConditionTrue,
						Message: "set by a concurrent writer",
					})
					return k8sClient.Status().Update(ctx, live)
				})).To(Succeed())
			}

			r := &reconciler.HelmOpStatusReconciler{
				Client: injector,
				Scheme: k8sClient.Scheme(),
			}

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: helmOpName})
			Expect(err).ToNot(HaveOccurred())

			By("leaving the concurrently written condition intact")
			live := &fleet.HelmOp{}
			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())
			cond, found := utils.FindCondition(live.Status.Conditions, fleet.HelmOpAcceptedCondition)
			Expect(found).To(BeTrue(), "Accepted condition was dropped by the status reconciler")
			Expect(cond.Message).To(Equal("set by a concurrent writer"))

			By("requeuing instead of overwriting the newer version")
			Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		})
	})

	When("no concurrent write happens", func() {
		It("still updates the status", func() {
			r := &reconciler.HelmOpStatusReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: helmOpName})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())

			live := &fleet.HelmOp{}
			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())
			cond, found := utils.FindCondition(live.Status.Conditions, "Ready")
			Expect(found).To(BeTrue(), "expected the status reconciler to write a Ready condition")
			Expect(cond.Status).To(Equal(corev1.ConditionTrue))
		})
	})
})

var _ = Describe("HelmOp polling job status conflicts", func() {
	var helmOpName types.NamespacedName

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})

		helmOpName = types.NamespacedName{Name: "polling-helmop", Namespace: namespace}
	})

	When("the polling job reads a HelmOp from a lagging cache", func() {
		It("does not drop conditions written since that read", func() {
			helmop := newHelmOp(helmOpName, "polling-test-shard", "alpine")
			helmop.Spec.Helm.Repo = newChartRepo()
			helmop.Spec.Helm.Version = "<= 0.2.0"
			helmop.Spec.PollingInterval = &metav1.Duration{Duration: time.Hour}
			Expect(k8sClient.Create(ctx, helmop)).To(Succeed())

			staleCopy := &fleet.HelmOp{}
			wrapped := &utils.StaleReadClient{
				Client:   k8sClient,
				Target:   helmOpName,
				CopyInto: copyHelmOpInto(staleCopy),
			}

			sched, err := quartz.NewStdScheduler()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { sched.Stop() })

			r := &reconciler.HelmOpReconciler{
				Client:    wrapped,
				Scheme:    k8sClient.Scheme(),
				Scheduler: sched,
				Workers:   1,
				Recorder:  &events.FakeRecorder{},
			}

			By("reconciling once so the polling job is scheduled with the wrapped client")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: helmOpName})
			Expect(err).ToNot(HaveOccurred())

			live := &fleet.HelmOp{}
			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())
			live.DeepCopyInto(staleCopy)

			By("having another controller publish a condition after that snapshot")
			live.Status.Conditions = append(live.Status.Conditions, genericcondition.GenericCondition{
				Type:    "ConcurrentWriter",
				Status:  corev1.ConditionTrue,
				Message: "set by a concurrent writer",
			})
			Expect(k8sClient.Status().Update(ctx, live)).To(Succeed())

			// The polling job reads the HelmOp twice before patching its status:
			// once up front, then again inside its retry loop. Serve the
			// pre-condition snapshot to both, so the job patches entirely from a
			// version that predates the concurrent write. Reads after that (the
			// retry) see the real object, as a cache that has caught up would.
			wrapped.Arm(2)

			By("running the scheduled polling job")
			scheduled, err := sched.GetScheduledJob(quartz.NewJobKey(string(staleCopy.UID)))
			Expect(err).ToNot(HaveOccurred())
			Expect(scheduled.JobDetail().Job().Execute(ctx)).To(Succeed())

			By("leaving the concurrently written condition intact")
			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())
			cond, found := utils.FindCondition(live.Status.Conditions, "ConcurrentWriter")
			Expect(found).To(BeTrue(), "condition was dropped by the polling job")
			Expect(cond.Message).To(Equal("set by a concurrent writer"))

			By("still recording the polling result")
			Expect(live.Status.Version).To(Equal("0.2.0"))
		})
	})
})

var _ = Describe("HelmOp reconciler status conflicts", func() {
	var helmOpName types.NamespacedName

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})

		helmOpName = types.NamespacedName{Name: "merge-helmop", Namespace: namespace}
	})

	When("another writer adds a condition while the reconcile is in flight", func() {
		It("does not drop it, and still publishes the resolved version", func() {
			// The reconciler used to rebuild the conditions array from the status
			// it read at the top of Reconcile. Its patch base is a fresh read, so
			// the optimistic lock never fires here; only merging against what it
			// read keeps a condition added since then from being dropped.
			//
			// The condition type is one no HelmOp controller writes, so the only
			// way it can survive is by being carried over from the live object.
			const foreignCondition = "ConcurrentWriter"

			helmop := newHelmOp(helmOpName, "merge-test-shard", "alpine")
			helmop.Spec.Helm.Repo = newChartRepo()
			helmop.Spec.Helm.Version = "0.1.0"
			Expect(k8sClient.Create(ctx, helmop)).To(Succeed())

			staleCopy := &fleet.HelmOp{}
			wrapped := &utils.StaleReadClient{
				Client:   k8sClient,
				Target:   helmOpName,
				CopyInto: copyHelmOpInto(staleCopy),
			}

			sched, err := quartz.NewStdScheduler()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { sched.Stop() })

			r := &reconciler.HelmOpReconciler{
				Client:    wrapped,
				Scheme:    k8sClient.Scheme(),
				Scheduler: sched,
				Workers:   1,
				Recorder:  &events.FakeRecorder{},
			}

			By("reconciling once so the finalizer and bundle are in place")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: helmOpName})
			Expect(err).ToNot(HaveOccurred())

			By("capturing the HelmOp as the next reconcile would read it")
			live := &fleet.HelmOp{}
			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())
			live.DeepCopyInto(staleCopy)

			By("having another controller add a condition after that read")
			live.Status.Conditions = append(live.Status.Conditions, genericcondition.GenericCondition{
				Type:    foreignCondition,
				Status:  corev1.ConditionTrue,
				Message: "set by a concurrent writer",
			})
			// Clear the version too, so the assertion below shows the reconciler
			// still republishes it. This is the case the removed #3883 workaround
			// used to handle, and it must survive a conflict retry.
			live.Status.Version = ""
			Expect(k8sClient.Status().Update(ctx, live)).To(Succeed())

			// Only the read at the top of Reconcile is stale. Everything after it,
			// including the read updateStatus patches from, sees the live object,
			// so the write itself does not conflict.
			wrapped.Arm(1)

			By("reconciling from that stale read")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: helmOpName})
			Expect(err).ToNot(HaveOccurred())

			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())

			By("having written its own status")
			_, found := utils.FindCondition(live.Status.Conditions, fleet.HelmOpAcceptedCondition)
			Expect(found).To(BeTrue(), "the reconciler did not write its own conditions, so the merge was not exercised")
			Expect(live.Status.Version).To(Equal("0.1.0"))

			By("leaving the concurrently written condition intact")
			cond, found := utils.FindCondition(live.Status.Conditions, foreignCondition)
			Expect(found).To(BeTrue(), "condition added since the reconcile's read was dropped")
			Expect(cond.Message).To(Equal("set by a concurrent writer"))
		})
	})
})

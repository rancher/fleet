package controller

import (
	"context"
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

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler"
	"github.com/rancher/fleet/internal/ssh"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
)

// isGitRepo matches GitRepo reads, for the test clients in integrationtests/utils.
func isGitRepo(obj client.Object) bool {
	_, ok := obj.(*v1alpha1.GitRepo)
	return ok
}

// copyGitRepoInto returns a CopyInto function serving the given stale GitRepo.
func copyGitRepoInto(stale *v1alpha1.GitRepo) func(client.Object) bool {
	return func(into client.Object) bool {
		gitrepo, ok := into.(*v1alpha1.GitRepo)
		if !ok {
			return false
		}
		stale.DeepCopyInto(gitrepo)

		return true
	}
}

var _ = Describe("GitRepo status conflicts", func() {
	var gitrepoName types.NamespacedName

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})

		gitrepoName = types.NamespacedName{Name: "conflict-gitrepo", Namespace: namespace}

		// The shard-ref label keeps the manager-run reconcilers (which use the
		// default, unsharded ShardID) away from this GitRepo, so the only writers
		// are the ones this test drives explicitly.
		gitrepo := &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gitrepoName.Name,
				Namespace: gitrepoName.Namespace,
				Labels:    map[string]string{sharding.ShardingRefLabel: "conflict-test-shard"},
			},
			Spec: v1alpha1.GitRepoSpec{
				Repo: "https://github.com/rancher/fleet-test-data",
			},
		}
		Expect(k8sClient.Create(ctx, gitrepo)).To(Succeed())
	})

	When("another controller writes a condition between the status reconciler's read and its patch", func() {
		It("does not drop that condition", func() {
			// Simulates the gitjob reconciler publishing Accepted=True while the
			// status reconciler is mid-flight, holding a copy that predates it.
			injector := &utils.ConflictInjector{
				Client: k8sClient,
				Target: gitrepoName,
				Match:  isGitRepo,
			}
			injector.Inject = func() {
				defer GinkgoRecover()
				Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
					live := &v1alpha1.GitRepo{}
					if err := k8sClient.Get(ctx, gitrepoName, live); err != nil {
						return err
					}
					live.Status.Conditions = append(live.Status.Conditions, genericcondition.GenericCondition{
						Type:    v1alpha1.GitRepoAcceptedCondition,
						Status:  corev1.ConditionTrue,
						Message: "set by a concurrent writer",
					})
					return k8sClient.Status().Update(ctx, live)
				})).To(Succeed())
			}

			r := &reconciler.StatusReconciler{
				Client: injector,
				Scheme: k8sClient.Scheme(),
			}

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: gitrepoName})
			Expect(err).ToNot(HaveOccurred())

			By("leaving the concurrently written condition intact")
			live := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, gitrepoName, live)).To(Succeed())
			cond, found := utils.FindCondition(live.Status.Conditions, v1alpha1.GitRepoAcceptedCondition)
			Expect(found).To(BeTrue(), "Accepted condition was dropped by the status reconciler")
			Expect(cond.Message).To(Equal("set by a concurrent writer"))

			By("requeuing instead of overwriting the newer version")
			Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		})
	})

	When("no concurrent write happens", func() {
		It("still updates the status", func() {
			r := &reconciler.StatusReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: gitrepoName})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())

			live := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, gitrepoName, live)).To(Succeed())
			Expect(live.Status.Display.State).To(Equal("GitUpdating"))
		})
	})
})

// stubFetcher is a GitFetcher returning a fixed commit.
type stubFetcher struct{ commit string }

func (f stubFetcher) LatestCommit(_ context.Context, _ *v1alpha1.GitRepo, _ client.Client) (string, error) {
	return f.commit, nil
}

var _ = Describe("GitRepo polling job status conflicts", func() {
	var gitrepoName types.NamespacedName

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})

		gitrepoName = types.NamespacedName{Name: "polling-gitrepo", Namespace: namespace}

		// See above: the shard-ref label keeps the manager-run reconcilers away.
		gitrepo := &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gitrepoName.Name,
				Namespace: gitrepoName.Namespace,
				Labels:    map[string]string{sharding.ShardingRefLabel: "polling-test-shard"},
			},
			Spec: v1alpha1.GitRepoSpec{
				Repo: "https://github.com/rancher/fleet-test-data",
			},
		}
		Expect(k8sClient.Create(ctx, gitrepo)).To(Succeed())
	})

	When("the polling job reads a GitRepo from a lagging cache", func() {
		It("does not drop conditions written since that read", func() {
			staleCopy := &v1alpha1.GitRepo{}
			wrapped := &utils.StaleReadClient{
				Client:   k8sClient,
				Target:   gitrepoName,
				CopyInto: copyGitRepoInto(staleCopy),
			}

			sched, err := quartz.NewStdScheduler()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { sched.Stop() })

			r := &reconciler.GitJobReconciler{
				Client:          wrapped,
				Scheme:          k8sClient.Scheme(),
				Image:           "image",
				Scheduler:       sched,
				GitFetcher:      stubFetcher{commit: "1883fd54bc5dfd225acf02aecbb6cb8020458e33"},
				Clock:           reconciler.RealClock{},
				Recorder:        &events.FakeRecorder{},
				Workers:         1,
				SystemNamespace: "default",
				KnownHosts:      ssh.KnownHosts{},
			}

			By("reconciling once so the polling job is scheduled with the wrapped client")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: gitrepoName})
			Expect(err).ToNot(HaveOccurred())

			gitrepo := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, gitrepoName, gitrepo)).To(Succeed())
			gitrepo.DeepCopyInto(staleCopy)

			By("having another controller publish a condition after that snapshot")
			gitrepo.Status.Conditions = append(gitrepo.Status.Conditions, genericcondition.GenericCondition{
				Type:    v1alpha1.GitRepoAcceptedCondition,
				Status:  corev1.ConditionTrue,
				Message: "set by a concurrent writer",
			})
			Expect(k8sClient.Status().Update(ctx, gitrepo)).To(Succeed())

			// The polling job reads the GitRepo twice before patching: once up
			// front, then again inside its retry loop. Serve the pre-condition
			// snapshot to both, so the job patches entirely from a version that
			// predates the concurrent write. Reads after that (the retry) see the
			// real object, as a cache that has caught up would.
			wrapped.Arm(2)

			By("running the scheduled polling job")
			scheduled, err := sched.GetScheduledJob(quartz.NewJobKey(string(gitrepo.UID)))
			Expect(err).ToNot(HaveOccurred())
			Expect(scheduled.JobDetail().Job().Execute(ctx)).To(Succeed())

			By("leaving the concurrently written condition intact")
			live := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, gitrepoName, live)).To(Succeed())
			cond, found := utils.FindCondition(live.Status.Conditions, v1alpha1.GitRepoAcceptedCondition)
			Expect(found).To(BeTrue(), "Accepted condition was dropped by the polling job")
			Expect(cond.Message).To(Equal("set by a concurrent writer"))

			By("still recording the polling result")
			Expect(live.Status.PollingCommit).To(Equal("1883fd54bc5dfd225acf02aecbb6cb8020458e33"))
		})
	})
})

var _ = Describe("GitRepo reconciler status conflicts", func() {
	var gitrepoName types.NamespacedName

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})

		gitrepoName = types.NamespacedName{Name: "merge-gitrepo", Namespace: namespace}

		gitrepo := &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gitrepoName.Name,
				Namespace: gitrepoName.Namespace,
				Labels:    map[string]string{sharding.ShardingRefLabel: "merge-test-shard"},
			},
			Spec: v1alpha1.GitRepoSpec{
				Repo: "https://github.com/rancher/fleet-test-data",
			},
		}
		Expect(k8sClient.Create(ctx, gitrepo)).To(Succeed())
	})

	When("another writer adds a condition while the reconcile is in flight", func() {
		It("does not drop it when writing the status", func() {
			// The reconciler rebuilds the conditions array from the status it read
			// at the top of Reconcile. Its patch base is a fresh read, so the
			// optimistic lock never fires here; only merging against what it read
			// keeps a condition added since then from being dropped.
			//
			// The condition type is one no GitRepo controller writes, so the only
			// way it can survive is by being carried over from the live object.
			const foreignCondition = "ConcurrentWriter"

			staleCopy := &v1alpha1.GitRepo{}
			wrapped := &utils.StaleReadClient{
				Client:   k8sClient,
				Target:   gitrepoName,
				CopyInto: copyGitRepoInto(staleCopy),
			}

			sched, err := quartz.NewStdScheduler()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { sched.Stop() })

			r := &reconciler.GitJobReconciler{
				Client:          wrapped,
				Scheme:          k8sClient.Scheme(),
				Image:           "image",
				Scheduler:       sched,
				GitFetcher:      stubFetcher{commit: "1883fd54bc5dfd225acf02aecbb6cb8020458e33"},
				Clock:           reconciler.RealClock{},
				Recorder:        &events.FakeRecorder{},
				Workers:         1,
				SystemNamespace: "default",
				KnownHosts:      ssh.KnownHosts{},
			}

			By("reconciling once so the finalizer and polling job are in place")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: gitrepoName})
			Expect(err).ToNot(HaveOccurred())

			By("capturing the GitRepo as the next reconcile would read it")
			gitrepo := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, gitrepoName, gitrepo)).To(Succeed())
			gitrepo.DeepCopyInto(staleCopy)

			By("having another controller add a condition after that read")
			gitrepo.Status.Conditions = append(gitrepo.Status.Conditions, genericcondition.GenericCondition{
				Type:    foreignCondition,
				Status:  corev1.ConditionTrue,
				Message: "set by a concurrent writer",
			})
			Expect(k8sClient.Status().Update(ctx, gitrepo)).To(Succeed())

			// Only the read at the top of Reconcile is stale. Everything after it,
			// including the read updateStatus patches from, sees the live object,
			// so the write itself does not conflict.
			wrapped.Arm(1)

			By("reconciling from that stale read")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: gitrepoName})
			Expect(err).ToNot(HaveOccurred())

			live := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, gitrepoName, live)).To(Succeed())

			By("having written its own status")
			_, found := utils.FindCondition(live.Status.Conditions, v1alpha1.GitRepoAcceptedCondition)
			Expect(found).To(BeTrue(), "the reconciler did not write its own conditions, so the merge was not exercised")

			By("leaving the concurrently written condition intact")
			cond, found := utils.FindCondition(live.Status.Conditions, foreignCondition)
			Expect(found).To(BeTrue(), "condition added since the reconcile's read was dropped")
			Expect(cond.Message).To(Equal("set by a concurrent writer"))
		})
	})

	When("the polling job records a new commit while the reconcile is in flight", func() {
		It("does not revert the polling fields when writing the status", func() {
			// PollingCommit and LastPollingTime belong to the polling job. On this
			// path the reconciler computes neither: polling is enabled and no
			// webhook is pending, so fetchLatestCommit never runs. Assigning them
			// from the status read at the top of Reconcile would therefore write
			// back values that predate the poll.
			//
			// That is not a lost update the optimistic lock can catch: the write
			// happens on a fresh read and never conflicts, it just carries stale
			// values. And because getNextCommit reads PollingCommit, reverting it
			// hides the new commit until the next poll.
			const polledCommit = "d87ef1f9ba09e0b8b0b06d18ac9c0f0e75d5e0aa"
			polledAt := metav1.NewTime(time.Now().Truncate(time.Second))

			staleCopy := &v1alpha1.GitRepo{}
			wrapped := &utils.StaleReadClient{
				Client:   k8sClient,
				Target:   gitrepoName,
				CopyInto: copyGitRepoInto(staleCopy),
			}

			sched, err := quartz.NewStdScheduler()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { sched.Stop() })

			r := &reconciler.GitJobReconciler{
				Client:          wrapped,
				Scheme:          k8sClient.Scheme(),
				Image:           "image",
				Scheduler:       sched,
				GitFetcher:      stubFetcher{commit: "1883fd54bc5dfd225acf02aecbb6cb8020458e33"},
				Clock:           reconciler.RealClock{},
				Recorder:        &events.FakeRecorder{},
				Workers:         1,
				SystemNamespace: "default",
				KnownHosts:      ssh.KnownHosts{},
			}

			By("reconciling once so the finalizer and polling job are in place")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: gitrepoName})
			Expect(err).ToNot(HaveOccurred())

			By("capturing the GitRepo as the next reconcile would read it")
			gitrepo := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, gitrepoName, gitrepo)).To(Succeed())
			gitrepo.DeepCopyInto(staleCopy)
			Expect(staleCopy.Status.PollingCommit).To(BeEmpty(), "the snapshot must predate the poll for this to test anything")

			By("having the polling job record a new commit after that read")
			gitrepo.Status.PollingCommit = polledCommit
			gitrepo.Status.LastPollingTime = polledAt
			Expect(k8sClient.Status().Update(ctx, gitrepo)).To(Succeed())

			// Only the read at the top of Reconcile is stale, as in the condition
			// case above: the read updateStatus writes from sees the live object.
			wrapped.Arm(1)

			By("reconciling from that stale read")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: gitrepoName})
			Expect(err).ToNot(HaveOccurred())

			live := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, gitrepoName, live)).To(Succeed())

			By("having written its own status")
			_, found := utils.FindCondition(live.Status.Conditions, v1alpha1.GitRepoAcceptedCondition)
			Expect(found).To(BeTrue(), "the reconciler did not write its status, so the polling fields were not exercised")

			By("leaving the polling result intact")
			Expect(live.Status.PollingCommit).To(Equal(polledCommit), "PollingCommit was reverted to the reconcile's stale read")
			Expect(live.Status.LastPollingTime.Time).To(BeTemporally("==", polledAt.Time), "LastPollingTime was reverted to the reconcile's stale read")
		})
	})
})

package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/helmops/reconciler"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("HelmOp resolved versions with stale status", func() {
	var helmOpName types.NamespacedName

	BeforeEach(func() {
		name, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})
		helmOpName = types.NamespacedName{Name: "version-helmop", Namespace: name}
	})

	DescribeTable("preserves the persisted version when the cached status is empty",
		func(polling, registryFailure bool) {
			var newerTagAvailable, registryUnavailable atomic.Bool
			var requests atomic.Int64
			svr := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/charts/alpine/tags/list" {
					http.NotFound(w, r)
					return
				}
				requests.Add(1)
				if registryUnavailable.Load() {
					http.Error(w, "chart tags unavailable", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if newerTagAvailable.Load() {
					fmt.Fprint(w, `{"name":"charts/alpine","tags":["0.1.0","0.2.0"]}`)
				} else {
					fmt.Fprint(w, `{"name":"charts/alpine","tags":["0.1.0"]}`)
				}
			}))
			DeferCleanup(svr.Close)

			helmop := newHelmOp(helmOpName, "stale-version-test-shard", "")
			helmop.Spec.Helm.Repo = "oci://" + strings.TrimPrefix(svr.URL, "https://") + "/charts/alpine"
			helmop.Spec.Helm.Version = "0.1.0"
			helmop.Spec.InsecureSkipTLSverify = true
			if polling {
				helmop.Spec.Helm.Version = "0.x.x"
				helmop.Spec.PollingInterval = &metav1.Duration{Duration: time.Hour}
			}
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

			By("persisting the initially resolved version through the real API")
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: helmOpName})
			Expect(err).ToNot(HaveOccurred())
			if polling {
				// Drive the job explicitly; the scheduler is never started, so
				// later registry changes cannot race the second reconcile.
				scheduled, err := sched.GetScheduledJob(quartz.NewJobKey(string(helmop.UID)))
				Expect(err).ToNot(HaveOccurred())
				Expect(scheduled.JobDetail().Job().Execute(ctx)).To(Succeed())
			}
			bundle := &fleet.Bundle{}
			Expect(k8sClient.Get(ctx, helmOpName, bundle)).To(Succeed())
			Expect(bundle.Spec.Helm.Version).To(Equal("0.1.0"))
			live := &fleet.HelmOp{}
			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())
			Expect(live.Status.Version).To(Equal("0.1.0"))

			By("reconciling a new spec while only the first status read lags")
			live.Spec.ForceSyncGeneration++
			Expect(k8sClient.Update(ctx, live)).To(Succeed())
			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())
			live.DeepCopyInto(staleCopy)
			staleCopy.Status.Version = ""
			wrapped.Arm(1)
			newerTagAvailable.Store(true)
			registryUnavailable.Store(registryFailure)
			previousRequests := requests.Load()

			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: helmOpName})
			if registryFailure {
				Expect(err).To(MatchError(ContainSubstring("could not get a chart version")))
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(k8sClient.Get(ctx, helmOpName, bundle)).To(Succeed())
			Expect(bundle.Spec.Helm.Version).To(Equal("0.1.0"))
			if registryFailure {
				Expect(bundle.Spec.ForceSyncGeneration).To(BeZero())
			} else {
				Expect(bundle.Spec.ForceSyncGeneration).To(Equal(live.Spec.ForceSyncGeneration))
			}
			Expect(k8sClient.Get(ctx, helmOpName, live)).To(Succeed())
			Expect(live.Status.Version).To(Equal("0.1.0"))
			if polling {
				Expect(requests.Load()).To(Equal(previousRequests), "only the polling job should resolve a new version")
			}
		},
		Entry("an exact OCI pin when a newer tag exists", false, false),
		Entry("a polling job's resolved version when a newer tag exists", true, false),
		Entry("an exact OCI pin when resolving its version fails", false, true),
	)
})

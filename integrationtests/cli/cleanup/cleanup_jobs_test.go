package cleanup

import (
	"fmt"
	"time"

	"github.com/rancher/fleet/internal/cmd/cli/cleanup"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Fleet CLI jobs cleanup", Ordered, func() {
	var (
		jobs []batchv1.Job
	)

	JustBeforeEach(func() {
		for _, c := range jobs {
			tmp := c.DeepCopy()
			err := k8sClient.Create(ctx, tmp)
			Expect(err).NotTo(HaveOccurred())

			tmp.Status = c.Status
			err = k8sClient.Status().Update(ctx, tmp)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	act := func(retention time.Duration) error {
		return cleanup.GitJobs(ctx, k8sClient, 1, retention)
	}

	When("cleaning up", func() {
		var otherns string

		BeforeEach(func() {
			otherns = namespace + "-other"
			Expect(k8sClient.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: otherns,
				},
			})).ToNot(HaveOccurred())

			owner1 := metav1.OwnerReference{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "GitRepo",
				Name:       "gitrepo-1",
				UID:        "1",
			}

			owner2 := metav1.OwnerReference{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "GitRepo",
				Name:       "gitrepo-2",
				UID:        "1",
			}

			owner3 := metav1.OwnerReference{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "GitRepo",
				Name:       "gitrepo-3",
				UID:        "1",
			}

			owner4 := metav1.OwnerReference{
				APIVersion: "something",
				Kind:       "somekind",
				Name:       "somename",
				UID:        "1",
			}

			spec := batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers: []corev1.Container{
							{
								Name:  "busybox",
								Image: "pause",
							},
						},
					},
				},
			}

			succeeded := func(t time.Time) batchv1.JobStatus {
				s := t.Add(-1 * time.Second)
				return batchv1.JobStatus{
					StartTime:      &metav1.Time{Time: s},
					CompletionTime: &metav1.Time{Time: t},
					Succeeded:      1,
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobComplete,
							Status: corev1.ConditionTrue,
						},
						{
							Type:   batchv1.JobSuccessCriteriaMet,
							Status: corev1.ConditionTrue,
						},
					},
				}
			}

			// list is sorted by latest to oldest
			jobs = []batchv1.Job{
				// one running job
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "job-running",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner1},
					},
					Spec: spec,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "job-old-1",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner1},
					},
					Spec:   spec,
					Status: succeeded(time.Now()),
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "job-old-2",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner1},
					},
					Spec:   spec,
					Status: succeeded(time.Now().Add(-1 * time.Hour)),
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "job-old-3",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner1},
					},
					Spec:   spec,
					Status: succeeded(time.Now().Add(-2 * time.Hour)),
				},
				// all succeeded, sharing namespace
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "another-job",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner2},
					},
					Spec:   spec,
					Status: succeeded(time.Now()),
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "another-job-old-1",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner2},
					},
					Spec:   spec,
					Status: succeeded(time.Now().Add(-1 * time.Hour)),
				},
				// separate namespace
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "job-1",
						Namespace:       otherns,
						OwnerReferences: []metav1.OwnerReference{owner3},
					},
					Spec:   spec,
					Status: succeeded(time.Now()),
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "job-old-2",
						Namespace:       otherns,
						OwnerReferences: []metav1.OwnerReference{owner3},
					},
					Spec:   spec,
					Status: succeeded(time.Now().Add(-1 * time.Hour)),
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "some-other-job",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner4},
					},
					Spec:   spec,
					Status: succeeded(time.Now()),
				},
			}
		})

		AfterEach(func() {
			// Clean up the other namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: otherns,
				},
			}
			_ = k8sClient.Delete(ctx, ns)
		})

		It("deletes all resources that have the right owner and succeeded", func() {
			Expect(act(0)).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				list := &batchv1.JobList{}
				err := k8sClient.List(ctx, list)
				g.Expect(err).NotTo(HaveOccurred())

				names := []string{}
				for _, cr := range list.Items {
					names = append(names, fmt.Sprintf("%s/%s", cr.Namespace, cr.Name))
				}
				g.Expect(names).To(ConsistOf(
					namespace+"/job-running",
					namespace+"/some-other-job",
				))
			}, 20*time.Second, 1*time.Second).Should(Succeed())
		})
	})

	When("cleaning up with retention for failed jobs", func() {
		BeforeEach(func() {
			owner := metav1.OwnerReference{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "GitRepo",
				Name:       "test-gitrepo",
				UID:        "test-uid",
			}

			spec := batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers: []corev1.Container{
							{
								Name:  "busybox",
								Image: "pause",
							},
						},
					},
				},
			}

			failed := func(t time.Time) batchv1.JobStatus {
				s := t.Add(-1 * time.Second)
				return batchv1.JobStatus{
					StartTime:      &metav1.Time{Time: s},
					CompletionTime: &metav1.Time{Time: t},
					Failed:         1,
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobFailed,
							Status: corev1.ConditionTrue,
						},
					},
				}
			}

			succeeded := func(t time.Time) batchv1.JobStatus {
				s := t.Add(-1 * time.Second)
				return batchv1.JobStatus{
					StartTime:      &metav1.Time{Time: s},
					CompletionTime: &metav1.Time{Time: t},
					Succeeded:      1,
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobComplete,
							Status: corev1.ConditionTrue,
						},
					},
				}
			}

			jobs = []batchv1.Job{
				// Running job - should never be deleted
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "retention-job-running",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner},
					},
					Spec: spec,
				},
				// Successful job - should always be deleted
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "retention-job-succeeded",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner},
					},
					Spec:   spec,
					Status: succeeded(time.Now().Add(-1 * time.Hour)),
				},
				// Failed job, old (2 hours) - should be deleted with 1h retention
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "retention-job-failed-old",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner},
					},
					Spec:   spec,
					Status: failed(time.Now().Add(-2 * time.Hour)),
				},
				// Failed job, recent (30 min) - should NOT be deleted with 1h retention
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "retention-job-failed-recent",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner},
					},
					Spec:   spec,
					Status: failed(time.Now().Add(-30 * time.Minute)),
				},
				// Failed job without completion time - should never be deleted
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "retention-job-failed-no-completion",
						Namespace:       namespace,
						OwnerReferences: []metav1.OwnerReference{owner},
					},
					Spec: spec,
					Status: batchv1.JobStatus{
						StartTime: &metav1.Time{Time: time.Now().Add(-3 * time.Hour)},
						Failed:    1,
					},
				},
			}
		})

		It("deletes failed jobs older than retention period", func() {
			Expect(act(1 * time.Hour)).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				list := &batchv1.JobList{}
				err := k8sClient.List(ctx, list)
				g.Expect(err).NotTo(HaveOccurred())

				names := []string{}
				for _, cr := range list.Items {
					names = append(names, cr.Name)
				}
				// Should keep: running, recent failed, failed without completion
				// Should delete: succeeded (always), old failed (> retention)
				g.Expect(names).To(ConsistOf(
					"retention-job-running",
					"retention-job-failed-recent",
					"retention-job-failed-no-completion",
				))
			}, 20*time.Second, 1*time.Second).Should(Succeed())
		})

		It("keeps all failed jobs when retention is zero", func() {
			Expect(act(0)).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				list := &batchv1.JobList{}
				err := k8sClient.List(ctx, list)
				g.Expect(err).NotTo(HaveOccurred())

				names := []string{}
				for _, cr := range list.Items {
					names = append(names, cr.Name)
				}
				// Should keep: running, all failed jobs
				// Should delete: succeeded only
				g.Expect(names).To(ConsistOf(
					"retention-job-running",
					"retention-job-failed-old",
					"retention-job-failed-recent",
					"retention-job-failed-no-completion",
				))
			}, 20*time.Second, 1*time.Second).Should(Succeed())
		})

		It("deletes all failed jobs when retention is very short", func() {
			Expect(act(1 * time.Second)).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				list := &batchv1.JobList{}
				err := k8sClient.List(ctx, list)
				g.Expect(err).NotTo(HaveOccurred())

				names := []string{}
				for _, cr := range list.Items {
					names = append(names, cr.Name)
				}
				// Should keep: running, failed without completion
				// Should delete: succeeded, all completed failed jobs
				g.Expect(names).To(ConsistOf(
					"retention-job-running",
					"retention-job-failed-no-completion",
				))
			}, 20*time.Second, 1*time.Second).Should(Succeed())
		})
	})
})

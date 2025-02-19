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
		jobs    []batchv1.Job
		otherns string
	)

	BeforeEach(func() {
		otherns = namespace + "-other"
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: otherns,
			},
		})).ToNot(HaveOccurred())
	})

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

	act := func() error {
		return cleanup.GitJobs(ctx, k8sClient, 1)
	}

	When("cleaning up", func() {
		BeforeEach(func() {
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

		It("deletes all resources that have the right owner and succeeded", func() {
			Expect(act()).NotTo(HaveOccurred())

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
})

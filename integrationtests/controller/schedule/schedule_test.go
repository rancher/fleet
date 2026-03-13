package schedule

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Schedule updates triggered by cluster updates", func() {
	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	When("a Cluster living in the same namespace as a schedule is updated to match the schedule's targets", func() {
		It("schedules the cluster", func() {
			By("creating the cluster and schedule")
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))

			schedule := v1alpha1.Schedule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-schedule",
					Namespace: namespace,
				},
				Spec: v1alpha1.ScheduleSpec{
					Schedule: "0 */1 * * * *", // Every minute
					Duration: metav1.Duration{Duration: 30 * time.Second},
					Targets: v1alpha1.ScheduleTargets{
						Clusters: []v1alpha1.ScheduleTarget{
							{
								ClusterSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{"can-be-scheduled": "yes"}, // initially doesn't match any cluster
								},
							},
						},
					},
				},
			}
			err = k8sClient.Create(ctx, &schedule)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "my-schedule"}, &schedule)
				g.Expect(err).NotTo(HaveOccurred())
			}).Should(Succeed())

			defer func() {
				Expect(k8sClient.Delete(ctx, &v1alpha1.Schedule{ObjectMeta: metav1.ObjectMeta{
					Name:      "my-schedule",
					Namespace: namespace,
				}})).NotTo(HaveOccurred())

			}()

			By("checking that the cluster has not been scheduled")
			cluster = &v1alpha1.Cluster{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cluster"}, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Scheduled).To(BeFalse())

			By("updating the cluster's labels to match the schedule's selector")
			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cluster"}, cluster)
				Expect(err).NotTo(HaveOccurred())
				cluster.Labels = map[string]string{"can-be-scheduled": "yes"}
				return k8sClient.Update(ctx, cluster)
			}).ShouldNot(HaveOccurred())

			By("validating that the cluster is scheduled")
			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cluster"}, cluster)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cluster.Status.Scheduled).To(BeTrue())
			}).Should(Succeed())
		})
	})

	When("another Cluster with a different shard ID and matching the schedule's targets is added into the same namespace", func() {
		It("schedules the cluster", func() {
			By("creating the cluster and schedule")
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))

			schedule := v1alpha1.Schedule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-schedule",
					Namespace: namespace,
				},
				Spec: v1alpha1.ScheduleSpec{
					Schedule: "0 */1 * * * *", // Every minute
					Duration: metav1.Duration{Duration: 30 * time.Second},
					Targets: v1alpha1.ScheduleTargets{
						Clusters: []v1alpha1.ScheduleTarget{
							{
								ClusterSelector: &metav1.LabelSelector{},
							},
						},
					},
				},
			}
			err = k8sClient.Create(ctx, &schedule)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "my-schedule"}, &schedule)
				g.Expect(err).NotTo(HaveOccurred())
			}).Should(Succeed())

			defer func() {
				Expect(k8sClient.Delete(ctx, &v1alpha1.Schedule{ObjectMeta: metav1.ObjectMeta{
					Name:      "my-schedule",
					Namespace: namespace,
				}})).NotTo(HaveOccurred())

			}()

			By("validating that the cluster is scheduled")
			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cluster"}, cluster)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cluster.Status.Scheduled).To(BeTrue())
			}).Should(Succeed())

			By("adding another cluster with a different shard ID to the same namespace")
			labels := map[string]string{"fleet.cattle.io/shard-ref": "different-shard"}
			shardedCluster, err := utils.CreateCluster(ctx, k8sClient, "cluster2", namespace, labels, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(shardedCluster).To(Not(BeNil()))

			By("validating that the cluster is scheduled")
			Eventually(func(g Gomega) {
				var shardedCluster v1alpha1.Cluster
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cluster2"}, &shardedCluster)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(shardedCluster.Status.Scheduled).To(BeTrue())
			}).Should(Succeed())
		})
	})
})

package clustergroup

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("ClusterGroup Status Fields", func() {

	var (
		clusterName = "test-cluster"
		groupName   = "test-cluster-group"
	)

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

	When("Cluster is added", func() {
		BeforeEach(func() {
			clusterGroup, err := createClusterGroup(groupName, namespace, &metav1.LabelSelector{
				MatchLabels: map[string]string{},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterGroup).To(Not(BeNil()))
		})

		It("updates the status fields", func() {
			clusterGroup := &v1alpha1.ClusterGroup{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: groupName}, clusterGroup)
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterGroup).To(Not(BeNil()))
			Expect(clusterGroup.Status.ClusterCount).To(Equal(0))
			Expect(clusterGroup.Status.Display.ReadyClusters).To(Equal(""))

			cluster, err := utils.CreateCluster(ctx, k8sClient, clusterName, namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))

			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: groupName}, clusterGroup)
				Expect(err).NotTo(HaveOccurred())
				clusterGroup.Spec.Selector.MatchLabels = map[string]string{
					"Name": clusterName,
				}
				return k8sClient.Update(ctx, clusterGroup)
			}).ShouldNot(HaveOccurred())
			Expect(clusterGroup.Status.ClusterCount).To(Equal(1))
			Expect(clusterGroup.Status.Display.ReadyClusters).To(Equal("1/1"))
		})
	})
})

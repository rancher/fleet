package cleanup

import (
	"time"

	"github.com/rancher/fleet/internal/cmd/cli/cleanup"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Fleet CLI clusterregistration cleanup", Ordered, func() {
	var (
		options              cleanup.Options
		clusters             []fleetv1.Cluster
		clusterregistrations []fleetv1.ClusterRegistration
	)

	JustBeforeEach(func() {
		for _, c := range clusters {
			c := c.DeepCopy()
			err := k8sClient.Create(ctx, c)
			Expect(err).NotTo(HaveOccurred())
		}
		for i := len(clusterregistrations) - 1; i >= 0; i-- {
			cr := clusterregistrations[i]
			tmp := &clusterregistrations[i]

			err := k8sClient.Create(ctx, tmp)
			Expect(err).NotTo(HaveOccurred())

			tmp.Status.Granted = cr.Status.Granted
			tmp.Status.ClusterName = cr.Status.ClusterName
			err = k8sClient.Status().Update(ctx, tmp)
			Expect(err).NotTo(HaveOccurred())

			// need to sleep, so resources have different creation times
			time.Sleep(1 * time.Second)
		}
	})

	act := func() error {
		return cleanup.ClusterRegistrations(ctx, k8sClient, options)
	}

	When("cleaning up", func() {
		BeforeEach(func() {
			options = cleanup.Options{Min: 1, Max: 1}
			clusters = []fleetv1.Cluster{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cluster-1",
						Namespace: namespace,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cluster-2",
						Namespace: namespace,
					},
				},
			}

			// list is sorted by latest to oldest
			clusterregistrations = []fleetv1.ClusterRegistration{
				{ // kept, controller might not have seen it yet
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-inflight",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{},
				},
				{ // kept, could be a new registration
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-inflight-cluster-1",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{
						ClusterName: "cluster-1",
					},
				},
				{ // kept
					ObjectMeta: metav1.ObjectMeta{
						Name:      "granted-cluster-1",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{
						Granted:     true,
						ClusterName: "cluster-1",
					},
				},
				{ // removed, cluster does not exist
					ObjectMeta: metav1.ObjectMeta{
						Name:      "granted-cluster-does-not-exist",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{
						Granted:     true,
						ClusterName: "cluster-does-not-exist",
					},
				},
				{ // removed, cluster does not exist
					ObjectMeta: metav1.ObjectMeta{
						Name:      "inflight-cluster-does-not-exist",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{
						ClusterName: "cluster-does-not-exist-2",
					},
				},
				{ // removed, cluster does not exist
					ObjectMeta: metav1.ObjectMeta{
						Name:      "granted-outdated-cluster-does-not-exist",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{
						Granted:     true,
						ClusterName: "cluster-does-not-exist",
					},
				},
				{ // kept, controller might not have seen it yet
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-old",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{},
				},
				{ // kept, could an ongoing registration
					ObjectMeta: metav1.ObjectMeta{
						Name:      "inflight-cluster-2",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{
						ClusterName: "cluster-2",
					},
				},
				{ // removed, there is a newer one
					ObjectMeta: metav1.ObjectMeta{
						Name:      "granted-outdated-cluster-1",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{
						Granted:     true,
						ClusterName: "cluster-1",
					},
				},
				{ // kept
					ObjectMeta: metav1.ObjectMeta{
						Name:      "granted-cluster-2",
						Namespace: namespace,
					},
					Status: fleetv1.ClusterRegistrationStatus{
						Granted:     true,
						ClusterName: "cluster-2",
					},
				},
			}
		})

		It("deletes all resources and leaves most recent ones", func() {
			Expect(act()).NotTo(HaveOccurred())

			clusterList := &fleetv1.ClusterList{}
			err := k8sClient.List(ctx, clusterList)
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterList.Items).To(HaveLen(2))

			clusters := []string{}
			for _, c := range clusterList.Items {
				clusters = append(clusters, c.Name)
			}
			Expect(clusters).To(ContainElements("cluster-1", "cluster-2"))

			registrationList := &fleetv1.ClusterRegistrationList{}
			err = k8sClient.List(ctx, registrationList)
			Expect(err).NotTo(HaveOccurred())

			cregs := []string{}
			for _, cr := range registrationList.Items {
				cregs = append(cregs, cr.Name)
			}
			Expect(cregs).To(ConsistOf(
				"empty-inflight",
				"empty-inflight-cluster-1",
				"granted-cluster-1",
				"empty-old",
				"inflight-cluster-2",
				"granted-cluster-2",
			))
		})
	})
})

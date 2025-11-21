package bundle

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Bundle UserID logging", func() {
	var (
		bundle    *v1alpha1.Bundle
		cluster   *v1alpha1.Cluster
		namespace string
	)

	createBundle := func(name, clusterName string, labels map[string]string) {
		bundle = &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Spec: v1alpha1.BundleSpec{
				BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: "default",
				},
				Targets: []v1alpha1.BundleTarget{
					{
						Name:        clusterName,
						ClusterName: clusterName,
					},
				},
				Resources: []v1alpha1.BundleResource{
					{
						Name:    "test-configmap.yaml",
						Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-cm\ndata:\n  key: value\n",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())
	}

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		logsBuffer.Reset()

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, bundle)).ToNot(HaveOccurred())
			Expect(k8sClient.Delete(ctx, cluster)).ToNot(HaveOccurred())
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	waitForReconciliation := func() {
		Eventually(func() int64 {
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: bundle.Namespace, Name: bundle.Name}, bundle)
			if err != nil {
				return 0
			}
			return bundle.Status.ObservedGeneration
		}).Should(BeNumerically(">", 0))

		Eventually(logsBuffer.String).Should(ContainSubstring(bundle.Name))
	}

	When("Bundle has user ID label", func() {
		const userID = "user-12345"

		BeforeEach(func() {
			var err error
			cluster, err = utils.CreateCluster(ctx, k8sClient, "test-cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())

			createBundle("test-bundle-with-userid", "test-cluster", map[string]string{
				v1alpha1.CreatedByUserIDLabel: userID,
			})
		})

		It("includes userID in log output", func() {
			waitForReconciliation()

			logs := logsBuffer.String()
			Expect(logs).To(Or(
				ContainSubstring(`"userID":"`+userID+`"`),
				ContainSubstring(`"userID": "`+userID+`"`),
			))

			Expect(logs).To(ContainSubstring("bundle"))
			Expect(logs).To(ContainSubstring(bundle.Name))
		})
	})

	When("Bundle does not have user ID label", func() {
		BeforeEach(func() {
			var err error
			cluster, err = utils.CreateCluster(ctx, k8sClient, "test-cluster-2", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())

			createBundle("test-bundle-without-userid", "test-cluster-2", nil)
		})

		It("does not include userID in log output", func() {
			waitForReconciliation()

			logs := logsBuffer.String()
			bundleLogs := utils.ExtractResourceLogs(logs, bundle.Name)
			Expect(bundleLogs).NotTo(ContainSubstring(`"userID"`))
		})
	})
})

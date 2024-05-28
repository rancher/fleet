package cluster

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Cluster Status Fields", func() {
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

	When("Bundledeployment is added", func() {
		BeforeEach(func() {
			cluster, err := createCluster("cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))

			gitrepo := &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo",
					Namespace: namespace,
				},
				Spec: v1alpha1.GitRepoSpec{
					Repo: "https://github.com/rancher/fleet-test-data/not-found",
				},
			}
			err = k8sClient.Create(ctx, gitrepo)
			Expect(err).NotTo(HaveOccurred())

			targets := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						TargetNamespace: "targetNs",
					},
					Name:        "cluster",
					ClusterName: "cluster",
				},
			}
			bundle, err := createBundle("name", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))
		})

		It("updates the status fields", func() {
			cluster := &v1alpha1.Cluster{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "cluster", Namespace: namespace}, cluster)
				Expect(err).NotTo(HaveOccurred())

				return cluster.Status.Summary.DesiredReady == 0 && cluster.Status.ReadyGitRepos == 0
			}).Should(BeTrue())
			Expect(cluster.Status.Summary.Ready).To(Equal(0))

			bundle := &v1alpha1.Bundle{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))
			Expect(bundle.Status.Display.State).To(Equal(""))

			By("Add the gitrepo name to bundle")
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).ToNot(HaveOccurred())
				bundle.Labels["fleet.cattle.io/repo-name"] = "test-gitrepo"
				return k8sClient.Update(ctx, bundle)
			}).ShouldNot(HaveOccurred())

			By("Prepare bundledeployment so the bundle gets to 'Ready' state")
			bd := &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "Ready"
				bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
				bd.Status.Ready = true
				bd.Status.NonModified = true
				return k8sClient.Status().Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				if err != nil {
					return err
				}
				bd.Labels["fleet.cattle.io/repo-name"] = "test-gitrepo"
				return k8sClient.Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			cluster = &v1alpha1.Cluster{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "cluster", Namespace: namespace}, cluster)
				Expect(err).NotTo(HaveOccurred())

				return cluster.Status.Summary.DesiredReady == 1 && cluster.Status.ReadyGitRepos == 1
			}).Should(BeTrue())
			Expect(cluster.Status.Display.ReadyBundles).To(Equal("1/1"))
			Expect(cluster.Status.Summary.Ready).To(Equal(1))
		})
	})
})

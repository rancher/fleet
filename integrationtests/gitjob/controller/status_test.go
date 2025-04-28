package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/integrationtests/utils"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("GitRepo Status Fields", func() {
	var (
		gitrepo *v1alpha1.GitRepo
		bd      *v1alpha1.BundleDeployment
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

	When("Bundle changes", func() {
		BeforeEach(func() {
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))
			targets := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						TargetNamespace: "targetNs",
					},
					Name:        "cluster",
					ClusterName: "cluster",
				},
			}
			bundle, err := utils.CreateBundle(ctx, k8sClient, "name", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))

			gitrepo = &v1alpha1.GitRepo{
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

			bd = &v1alpha1.BundleDeployment{}
			Eventually(func() bool {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				return err == nil
			}).Should(BeTrue())
		})

		It("updates the status fields", func() {
			bundle := &v1alpha1.Bundle{}
			bundleName := types.NamespacedName{Namespace: namespace, Name: "name"}
			gitrepoName := types.NamespacedName{Namespace: namespace, Name: gitrepo.Name}
			By("Receiving a bundle update")
			Eventually(func() error {
				err := k8sClient.Get(ctx, bundleName, bundle)
				Expect(err).ToNot(HaveOccurred())
				bundle.Labels["fleet.cattle.io/repo-name"] = gitrepo.Name
				return k8sClient.Update(ctx, bundle)
			}).ShouldNot(HaveOccurred())
			Expect(bundle.Status.Summary.Ready).ToNot(Equal(1))

			err := k8sClient.Get(ctx, gitrepoName, gitrepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(gitrepo.Status.Summary.Ready).To(Equal(0))
			Expect(gitrepo.Status.ReadyClusters).To(Equal(0))

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, gitrepoName, gitrepo)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gitrepo.Status.DesiredReadyClusters).To(Equal(1))
			}).Should(Succeed())

			// This simulates what the bundle deployment reconciler would do.
			By("Updating the BundleDeployment status to ready")
			bd := &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, bundleName, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "Ready"
				bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
				bd.Status.Ready = true
				bd.Status.NonModified = true
				return k8sClient.Status().Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			// waiting for the bundle to update
			Eventually(func() bool {
				err := k8sClient.Get(ctx, bundleName, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 1
			}).Should(BeTrue())

			// waiting for the GitRepo to update
			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, gitrepoName, gitrepo)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gitrepo.Status.Summary.Ready).To(Equal(1))
				g.Expect(gitrepo.Status.ReadyClusters).To(Equal(1))
				g.Expect(gitrepo.Status.DesiredReadyClusters).To(Equal(1))
			}).Should(Succeed())

			By("Deleting a bundle")
			err = k8sClient.Delete(ctx, bundle)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, gitrepoName, gitrepo)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gitrepo.Status.Summary.Ready).To(Equal(0))
				g.Expect(gitrepo.Status.Summary.DesiredReady).To(Equal(0))
				g.Expect(gitrepo.Status.Display.ReadyBundleDeployments).To(Equal("0/0"))
			}).Should(Succeed())
		})
	})
})

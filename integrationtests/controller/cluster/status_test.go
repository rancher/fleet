package cluster

import (
	"fmt"

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

	When("Bundledeployments are added", func() {
		BeforeEach(func() {
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))

			gitrepo := &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo",
					Namespace: namespace,
				},
				Spec: v1alpha1.GitRepoSpec{
					Repo: "https://github.com/rancher/fleet-test-data/does-not-matter",
				},
			}
			err = k8sClient.Create(ctx, gitrepo)
			Expect(err).NotTo(HaveOccurred())

			helmop := &v1alpha1.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-helmop",
					Namespace: namespace,
				},
				Spec: v1alpha1.HelmOpSpec{
					BundleSpec: v1alpha1.BundleSpec{
						BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
							Helm: &v1alpha1.HelmOptions{
								Chart: "https://github.com/rancher/fleet-test-data/does-not-matter.tgz",
							},
						},
					},
				},
			}
			err = k8sClient.Create(ctx, helmop)
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
			gitBundle, err := utils.CreateBundle(ctx, k8sClient, "gitrepo-bundle", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(gitBundle).To(Not(BeNil()))

			helmBundle, err := utils.CreateBundle(ctx, k8sClient, "helmop-bundle", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(helmBundle).To(Not(BeNil()))
		})

		It("updates the status fields", func() {
			cluster := &v1alpha1.Cluster{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "cluster", Namespace: namespace}, cluster)
				Expect(err).NotTo(HaveOccurred())

				return cluster.Status.Summary.DesiredReady == 0 && cluster.Status.ReadyGitRepos == 0
			}).Should(BeTrue())
			Expect(cluster.Status.Summary.Ready).To(Equal(0))

			gBundle := &v1alpha1.Bundle{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "gitrepo-bundle"}, gBundle)
			Expect(err).NotTo(HaveOccurred())
			Expect(gBundle).To(Not(BeNil()))
			Expect(gBundle.Status.Display.State).To(Equal(""))

			hBundle := &v1alpha1.Bundle{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "helmop-bundle"}, hBundle)
			Expect(err).NotTo(HaveOccurred())
			Expect(hBundle).To(Not(BeNil()))
			Expect(hBundle.Status.Display.State).To(Equal(""))

			By("Adding the gitrepo name to the git bundle")
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "gitrepo-bundle"}, gBundle)
				Expect(err).ToNot(HaveOccurred())
				gBundle.Labels["fleet.cattle.io/repo-name"] = "test-gitrepo"
				return k8sClient.Update(ctx, gBundle)
			}).ShouldNot(HaveOccurred())

			By("Adding the helmop name to the Helm bundle")
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "helmop-bundle"}, hBundle)
				Expect(err).ToNot(HaveOccurred())
				hBundle.Labels["fleet.cattle.io/fleet-helm-name"] = "test-helmop"
				return k8sClient.Update(ctx, hBundle)
			}).ShouldNot(HaveOccurred())

			By("Preparing the bundledeployments so the bundles get to 'Ready' state")
			bd := &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "gitrepo-bundle"}, bd)
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
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "gitrepo-bundle"}, bd)
				if err != nil {
					return err
				}
				bd.Labels["fleet.cattle.io/repo-name"] = "test-gitrepo"
				return k8sClient.Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			bd = &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "helmop-bundle"}, bd)
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
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "helmop-bundle"}, bd)
				if err != nil {
					return err
				}
				bd.Labels["fleet.cattle.io/fleet-helm-name"] = "test-helmop"
				return k8sClient.Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			cluster = &v1alpha1.Cluster{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "cluster", Namespace: namespace}, cluster)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(cluster.Status.Summary.DesiredReady).To(Equal(2))
				g.Expect(cluster.Status.DesiredReadyGitRepos).To(
					Equal(1),
					fmt.Sprintf("got %d desired ready GitRepos, expected 1", cluster.Status.DesiredReadyGitRepos),
				)
				g.Expect(cluster.Status.ReadyGitRepos).To(
					Equal(1),
					fmt.Sprintf("got %d ready GitRepos, expected 1", cluster.Status.ReadyGitRepos),
				)
				g.Expect(cluster.Status.DesiredReadyHelmOps).To(
					Equal(1),
					fmt.Sprintf("got %d desired ready HelmOps, expected 1", cluster.Status.DesiredReadyHelmOps),
				)
				g.Expect(cluster.Status.ReadyHelmOps).To(
					Equal(1),
					fmt.Sprintf("got %d ready HelmOps, expected 1", cluster.Status.ReadyHelmOps),
				)
			}).Should(Succeed())
			Expect(cluster.Status.Display.ReadyBundles).To(Equal("2/2"))
			Expect(cluster.Status.Summary.Ready).To(Equal(2))
		})
	})
})

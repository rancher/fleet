package gitrepo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
			cluster, err := createCluster("cluster", namespace, nil, namespace)
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
			bundle, err := createBundle("name", namespace, targets, targets)
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
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).ToNot(HaveOccurred())
				bundle.Labels["fleet.cattle.io/repo-name"] = gitrepo.Name
				return k8sClient.Update(ctx, bundle)
			}).ShouldNot(HaveOccurred())
			Expect(bundle.Status.Summary.Ready).ToNot(Equal(1))

			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitrepo.Name}, gitrepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(gitrepo.Status.Summary.Ready).To(Equal(0))

			bd := &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "Ready"
				bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
				bd.Status.Ready = true
				bd.Status.NonModified = true
				return k8sClient.Status().Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 1
			}).Should(BeTrue())
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitrepo.Name}, gitrepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(gitrepo.Status.Summary.Ready).To(Equal(1))
		})
	})
})

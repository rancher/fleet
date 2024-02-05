package gitrepo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("GitRepo", Ordered, func() {
	BeforeAll(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	var gitrepo *v1alpha1.GitRepo

	BeforeEach(func() {
		gitrepo = &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gitrepo",
				Namespace: namespace,
			},
			Spec: v1alpha1.GitRepoSpec{
				Repo: "https://github.com/rancher/fleet-test-data/not-found",
			},
		}

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, gitrepo)).ToNot(HaveOccurred())
		})
	})

	When("creating a gitrepo", func() {
		JustBeforeEach(func() {
			err := k8sClient.Create(ctx, gitrepo)
			Expect(err).NotTo(HaveOccurred())
		})

		It("creates RBAC resources", func() {
			Expect(gitrepo.Spec.PollingInterval).To(BeNil())

			Eventually(func() bool {
				ns := types.NamespacedName{Name: "git-test-gitrepo", Namespace: namespace}

				if err := k8sClient.Get(ctx, ns, &corev1.ServiceAccount{}); err != nil {
					return false
				}
				if err := k8sClient.Get(ctx, ns, &rbacv1.Role{}); err != nil {
					return false
				}
				if err := k8sClient.Get(ctx, ns, &rbacv1.RoleBinding{}); err != nil {
					return false
				}

				return true
			}).Should(BeTrue())
		})

		It("updates the gitrepo status", func() {
			org := gitrepo.ResourceVersion
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: "test-gitrepo", Namespace: namespace}, gitrepo)
				return gitrepo.ResourceVersion > org
			}).Should(BeTrue())

			Expect(gitrepo.Status.Display.ReadyBundleDeployments).To(Equal("0/0"))
			Expect(gitrepo.Status.Display.State).To(Equal("GitUpdating"))
			Expect(gitrepo.Status.Display.Error).To(BeFalse())
			Expect(gitrepo.Status.Conditions).To(HaveLen(1))
			Expect(gitrepo.Status.Conditions[0].Type).To(Equal("Accepted"))
			Expect(string(gitrepo.Status.Conditions[0].Status)).To(Equal("True"))
			Expect(gitrepo.Status.DeepCopy().ObservedGeneration).To(Equal(int64(1)))
		})
	})
})

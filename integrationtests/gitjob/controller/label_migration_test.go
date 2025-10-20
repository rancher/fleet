package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("GitRepo label migration", func() {
	var namespace string

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		})).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).ToNot(HaveOccurred())
		})
	})

	createGitRepo := func(name string, labels map[string]string) {
		gitrepo := &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Spec: v1alpha1.GitRepoSpec{
				Repo: "https://github.com/rancher/fleet-test-data/not-found",
			},
		}
		Expect(k8sClient.Create(ctx, gitrepo)).ToNot(HaveOccurred())
	}

	DescribeTable("should remove deprecated label after migration",
		func(gitRepoName string, initialLabels map[string]string) {
			const deprecatedLabel = "fleet.cattle.io/created-by-display-name"

			createGitRepo(gitRepoName, initialLabels)
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, &v1alpha1.GitRepo{
					ObjectMeta: metav1.ObjectMeta{Name: gitRepoName, Namespace: namespace},
				})).ToNot(HaveOccurred())
			})

			Eventually(func(g Gomega) {
				gitrepo := &v1alpha1.GitRepo{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitRepoName}, gitrepo)).To(Succeed())
				g.Expect(gitrepo.Status.ObservedGeneration).To(BeNumerically(">", 0))
			}).Should(Succeed())

			gitrepo := &v1alpha1.GitRepo{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitRepoName}, gitrepo)).To(Succeed())

			Expect(gitrepo.Labels).ToNot(HaveKey(deprecatedLabel))
			Expect(gitrepo.Labels).To(HaveKey(v1alpha1.CreatedByUserIDLabel))
		},
		Entry("with label present initially",
			"gitrepo-with-label",
			map[string]string{
				"fleet.cattle.io/created-by-display-name": "admin",
				v1alpha1.CreatedByUserIDLabel:             "user-12345",
			},
		),
		Entry("without label present initially",
			"gitrepo-without-label",
			map[string]string{
				v1alpha1.CreatedByUserIDLabel: "user-12345",
			},
		),
	)
})

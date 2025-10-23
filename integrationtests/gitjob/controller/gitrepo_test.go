package controller

import (
	"encoding/hex"
	"fmt"
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/integrationtests/utils"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("GitRepo", func() {
	var (
		gitrepo     *v1alpha1.GitRepo
		gitrepoName string
	)

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		p := make([]byte, 12)
		s := rand.New(rand.NewSource(GinkgoRandomSeed())) // nolint:gosec // non-crypto usage
		if _, err := s.Read(p); err != nil {
			panic(err)
		}
		gitrepoName = fmt.Sprintf("test-gitrepo-%.12s", hex.EncodeToString(p))

		gitrepo = &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gitrepoName,
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
			expectedCommit = commit
			err := k8sClient.Create(ctx, gitrepo)
			Expect(err).NotTo(HaveOccurred())
		})

		It("updates the gitrepo status", func() {
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: gitrepoName, Namespace: namespace}, gitrepo)
				g.Expect(gitrepo.Status.Display.ReadyBundleDeployments).To(Equal("0/0"))
				g.Expect(gitrepo.Status.Display.Error).To(BeFalse())
				g.Expect(gitrepo.Status.Conditions).To(HaveLen(5))
				g.Expect(checkCondition(gitrepo, "GitPolling", corev1.ConditionTrue, "")).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Reconciling", corev1.ConditionFalse, "")).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Stalled", corev1.ConditionFalse, "")).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Ready", corev1.ConditionTrue, "")).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Accepted", corev1.ConditionTrue, "")).To(BeTrue())
			}).Should(Succeed())
		})
	})
})

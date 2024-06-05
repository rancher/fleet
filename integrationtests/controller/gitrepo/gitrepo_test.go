package gitrepo

import (
	"encoding/hex"
	"fmt"
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
			err := k8sClient.Create(ctx, gitrepo)
			Expect(err).NotTo(HaveOccurred())
		})

		It("updates the gitrepo status", func() {
			org := gitrepo.ResourceVersion
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: gitrepoName, Namespace: namespace}, gitrepo)
				return gitrepo.ResourceVersion > org &&
					gitrepo.Status.Display.ReadyBundleDeployments == "0/0" &&
					gitrepo.Status.Display.State == "GitUpdating" &&
					!gitrepo.Status.Display.Error &&
					len(gitrepo.Status.Conditions) == 2 &&
					gitrepo.Status.Conditions[0].Type == "Ready" &&
					string(gitrepo.Status.Conditions[0].Status) == "True" &&
					gitrepo.Status.Conditions[1].Type == "Accepted" &&
					string(gitrepo.Status.Conditions[1].Status) == "True" &&
					gitrepo.Status.DeepCopy().ObservedGeneration == int64(1)
			}).Should(BeTrue())
		})
	})
})

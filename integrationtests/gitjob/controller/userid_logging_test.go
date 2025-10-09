package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("GitRepo UserID logging", func() {
	var (
		gitrepo   *fleet.GitRepo
		namespace string
	)

	createGitRepo := func(name string, labels map[string]string) {
		gitrepo = &fleet.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Spec: fleet.GitRepoSpec{
				Repo: "https://github.com/rancher/fleet-test-data/single-path",
			},
		}
		Expect(k8sClient.Create(ctx, gitrepo)).ToNot(HaveOccurred())
	}

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		logsBuffer.Reset()

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, gitrepo)).ToNot(HaveOccurred())
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	When("GitRepo has user ID label", func() {
		const userID = "user-12345"

		BeforeEach(func() {
			createGitRepo("test-gitrepo-with-userid", map[string]string{
				fleet.CreatedByUserIDLabel: userID,
			})
		})

		It("includes userID in log output", func() {
			Eventually(func() string {
				return logsBuffer.String()
			}, timeout).Should(Or(
				ContainSubstring(`"userID":"`+userID+`"`),
				ContainSubstring(`"userID": "`+userID+`"`),
			))

			logs := logsBuffer.String()
			Expect(logs).To(ContainSubstring("gitjob"))
			Expect(logs).To(ContainSubstring(gitrepo.Name))
		})
	})

	When("GitRepo does not have user ID label", func() {
		BeforeEach(func() {
			createGitRepo("test-gitrepo-without-userid", nil)
		})

		It("does not include userID in log output", func() {
			Eventually(func() string {
				return logsBuffer.String()
			}, timeout).Should(ContainSubstring(gitrepo.Name))

			logs := logsBuffer.String()
			gitrepoLogs := utils.ExtractResourceLogs(logs, gitrepo.Name)
			Expect(gitrepoLogs).NotTo(ContainSubstring(`"userID"`))
		})
	})
})

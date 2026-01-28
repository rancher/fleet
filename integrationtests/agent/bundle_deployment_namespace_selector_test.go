package agent_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func hasCondition(bd *v1alpha1.BundleDeployment, condType string, status corev1.ConditionStatus) bool {
	for _, cond := range bd.Status.Conditions {
		if cond.Type == condType && cond.Status == status {
			return true
		}
	}
	return false
}

func getConditionMessage(bd *v1alpha1.BundleDeployment, condType string, status corev1.ConditionStatus) string {
	for _, cond := range bd.Status.Conditions {
		if cond.Type == condType && cond.Status == status {
			return cond.Message
		}
	}
	return ""
}

var _ = Describe("BundleDeployment namespace selector validation", Ordered, func() {
	var (
		namespace             string
		testNamespaceMatching string
		testNamespaceNotMatch string
	)

	BeforeAll(func() {
		namespace = createNamespace()

		testNamespaceMatching = namespace + "-matching"
		nsMatching := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespaceMatching,
				Labels: map[string]string{
					"env":  "production",
					"team": "platform",
				},
			},
		}
		err := k8sClient.Create(context.TODO(), nsMatching)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = k8sClient.Delete(context.TODO(), nsMatching)
		})

		testNamespaceNotMatch = namespace + "-notmatch"
		nsNotMatch := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespaceNotMatch,
				Labels: map[string]string{
					"env":  "development",
					"team": "backend",
				},
			},
		}
		err = k8sClient.Create(context.TODO(), nsNotMatch)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = k8sClient.Delete(context.TODO(), nsNotMatch)
		})
	})

	When("BundleDeployment has no namespace selector", func() {
		It("deploys successfully to any namespace", func() {
			bd := &v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-no-selector",
					Namespace: clusterNS,
				},
				Spec: v1alpha1.BundleDeploymentSpec{
					DeploymentID: "v1",
					Options: v1alpha1.BundleDeploymentOptions{
						DefaultNamespace: testNamespaceMatching,
					},
				},
			}

			err := k8sClient.Create(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				return err == nil && bd.Status.Ready
			}).Should(BeTrue())

			err = k8sClient.Delete(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	When("BundleDeployment has namespace selector matching target namespace labels", func() {
		It("deploys successfully", func() {
			bd := &v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-selector-match",
					Namespace: clusterNS,
				},
				Spec: v1alpha1.BundleDeploymentSpec{
					DeploymentID: "v1",
					Options: v1alpha1.BundleDeploymentOptions{
						DefaultNamespace: testNamespaceMatching,
						AllowedTargetNamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"env":  "production",
								"team": "platform",
							},
						},
					},
				},
			}

			err := k8sClient.Create(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				if err != nil {
					return false
				}

				return hasCondition(bd, "Deployed", corev1.ConditionTrue) || bd.Status.Ready
			}).Should(BeTrue())

			err = k8sClient.Delete(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	When("BundleDeployment has namespace selector not matching target namespace labels", func() {
		It("fails deployment with clear error", func() {
			bd := &v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-selector-nomatch",
					Namespace: clusterNS,
				},
				Spec: v1alpha1.BundleDeploymentSpec{
					DeploymentID: "v1",
					Options: v1alpha1.BundleDeploymentOptions{
						DefaultNamespace: testNamespaceNotMatch,
						AllowedTargetNamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"env":  "production",
								"team": "platform",
							},
						},
					},
				},
			}

			err := k8sClient.Create(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				if err != nil {
					return false
				}

				return hasCondition(bd, "Deployed", corev1.ConditionFalse)
			}).Should(BeTrue())

			err = k8sClient.Get(context.TODO(), types.NamespacedName{
				Name:      bd.Name,
				Namespace: bd.Namespace,
			}, bd)
			Expect(err).ToNot(HaveOccurred())

			message := getConditionMessage(bd, "Deployed", corev1.ConditionFalse)
			Expect(message).To(ContainSubstring("does not match AllowedTargetNamespaceSelector"))

			err = k8sClient.Delete(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

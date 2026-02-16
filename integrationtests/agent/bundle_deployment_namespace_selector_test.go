package agent_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func hasDeployedCondition(bd *v1alpha1.BundleDeployment, status corev1.ConditionStatus) bool {
	for _, cond := range bd.Status.Conditions {
		if cond.Type == "Deployed" && cond.Status == status {
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

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(bd.Status.Ready).To(BeTrue())
			}).Should(Succeed())

			err = k8sClient.Delete(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())
		})

		// Regression test: Verify that deployments without AllowedTargetNamespaceSelector
		// can still deploy to missing namespaces via Helm's CreateNamespace feature.
		// Without the selector guard, the namespace existence check would run unconditionally
		// and prevent Helm from creating the namespace, breaking existing deployments.
		It("deploys successfully to a missing namespace", func() {
			missingNamespace := namespace + "-missing-no-selector"
			bd := &v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-no-selector-missing-namespace",
					Namespace: clusterNS,
				},
				Spec: v1alpha1.BundleDeploymentSpec{
					DeploymentID: "v1",
					Options: v1alpha1.BundleDeploymentOptions{
						DefaultNamespace: missingNamespace,
					},
				},
			}

			err := k8sClient.Create(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hasDeployedCondition(bd, corev1.ConditionTrue) || bd.Status.Ready).To(BeTrue())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				ns := &corev1.Namespace{}
				err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: missingNamespace}, ns)
				g.Expect(err).NotTo(HaveOccurred())
			}).Should(Succeed())

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

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hasDeployedCondition(bd, corev1.ConditionTrue) || bd.Status.Ready).To(BeTrue())
			}).Should(Succeed())

			err = k8sClient.Delete(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())
		})

		// Verify that TargetNamespace takes precedence over DefaultNamespace per GetDeploymentNS logic
		It("validates TargetNamespace when both TargetNamespace and DefaultNamespace are set", func() {
			bd := &v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-selector-targetnamespace",
					Namespace: clusterNS,
				},
				Spec: v1alpha1.BundleDeploymentSpec{
					DeploymentID: "v1",
					Options: v1alpha1.BundleDeploymentOptions{
						TargetNamespace:  testNamespaceMatching, // This should take precedence
						DefaultNamespace: testNamespaceNotMatch, // This should be ignored
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

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hasDeployedCondition(bd, corev1.ConditionTrue) || bd.Status.Ready).To(BeTrue())
			}).Should(Succeed())

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

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hasDeployedCondition(bd, corev1.ConditionFalse)).To(BeTrue())
			}).Should(Succeed())

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

	When("BundleDeployment has namespace selector and target namespace is missing", func() {
		It("fails deployment with clear error", func() {
			missingNamespace := namespace + "-missing"
			bd := &v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-selector-missing-namespace",
					Namespace: clusterNS,
				},
				Spec: v1alpha1.BundleDeploymentSpec{
					DeploymentID: "v1",
					Options: v1alpha1.BundleDeploymentOptions{
						DefaultNamespace: missingNamespace,
						AllowedTargetNamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"env": "production",
							},
						},
					},
				},
			}

			err := k8sClient.Create(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      bd.Name,
					Namespace: bd.Namespace,
				}, bd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hasDeployedCondition(bd, corev1.ConditionFalse)).To(BeTrue())
			}).Should(Succeed())

			err = k8sClient.Get(context.TODO(), types.NamespacedName{
				Name:      bd.Name,
				Namespace: bd.Namespace,
			}, bd)
			Expect(err).ToNot(HaveOccurred())

			message := getConditionMessage(bd, "Deployed", corev1.ConditionFalse)
			Expect(message).To(Equal(fmt.Sprintf("target namespace %s does not exist on downstream cluster", missingNamespace)))

			err = k8sClient.Delete(context.TODO(), bd)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

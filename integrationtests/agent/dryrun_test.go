package agent

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("DryRun determines difference between desired state and actual", func() {
	setID := "set123"
	ctx := context.Background()

	When("a pod changes", func() {
		var pod *corev1.Pod

		BeforeEach(func() {
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{},
			}
			labels, annotations, _ := desiredset.GetLabelsAndAnnotations(setID)
			pod.SetLabels(labels)
			pod.SetAnnotations(annotations)
		})

		It("reflects in the plan", func() {
			plan, err := dsClient.Plan(ctx, "default", "set123", pod)
			Expect(err).ToNot(HaveOccurred())

			Expect(plan.Create).To(HaveLen(1))
			Expect(plan.Delete).To(HaveLen(1))
			Expect(plan.Update).To(HaveLen(0))
			Expect(plan.Objects).To(HaveLen(0))

			By("creating the pod", func() {
				pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
					Name:  "test",
					Image: "test",
				})

				_ = k8sClient.Create(context.Background(), pod)
				DeferCleanup(func() {
					_ = k8sClient.Delete(context.Background(), pod)
				})

				plan, err = dsClient.Plan(ctx, "default", "set123", pod)
				Expect(err).ToNot(HaveOccurred())

				Expect(plan.Create).To(HaveLen(1))
				Expect(plan.Delete).To(HaveLen(1))
				Expect(plan.Update).To(HaveLen(0))
				Expect(plan.Objects).To(HaveLen(1))
			})

			By("modifying the desired state", func() {
				pod.Spec.Containers[0].Command = []string{"echo", "test"}

				plan, err = dsClient.Plan(ctx, "default", "set123", pod)
				Expect(err).ToNot(HaveOccurred())

				Expect(plan.Create).To(HaveLen(1))
				Expect(plan.Delete).To(HaveLen(1))
				Expect(plan.Update).To(HaveLen(1))
				Expect(plan.Objects).To(HaveLen(1))
			})
		})
	})

	When("a ConfigMap changes", func() {
		var cm *corev1.ConfigMap

		BeforeEach(func() {
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Data: map[string]string{},
			}
		})

		It("reflects in the plan", func() {
			labels, annotations, err := desiredset.GetLabelsAndAnnotations(setID)
			Expect(err).ToNot(HaveOccurred())
			cm.SetLabels(labels)
			cm.SetAnnotations(annotations)

			plan, err := dsClient.Plan(ctx, "default", "set123", cm)
			Expect(err).ToNot(HaveOccurred())

			Expect(plan.Create).To(HaveLen(1))
			Expect(plan.Delete).To(HaveLen(1))
			Expect(plan.Update).To(HaveLen(0))
			Expect(plan.Objects).To(HaveLen(0))

			By("creating the cm", func() {
				err := k8sClient.Create(context.Background(), cm)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() {
					_ = k8sClient.Delete(context.Background(), cm)
				})

				plan, err = dsClient.Plan(ctx, "default", "set123", cm)
				Expect(err).ToNot(HaveOccurred())

				Expect(plan.Create).To(HaveLen(1))
				Expect(plan.Delete).To(HaveLen(1))
				Expect(plan.Update).To(HaveLen(0))
				Expect(plan.Objects).To(HaveLen(1))
			})

			By("modifying the desired state", func() {
				cm.Data = map[string]string{"test": "test"}

				plan, err = dsClient.Plan(ctx, "default", "set123", cm)
				Expect(err).ToNot(HaveOccurred())

				Expect(plan.Create).To(HaveLen(1))
				Expect(plan.Delete).To(HaveLen(1))
				Expect(plan.Update).To(HaveLen(1))
				Expect(plan.Objects).To(HaveLen(1))
			})
		})

		It("should ignore configmap without wrangler labels", func() {
			plan, err := dsClient.Plan(ctx, "default", "set123", cm)
			Expect(err).ToNot(HaveOccurred())

			Expect(plan.Create).To(HaveLen(1))
			Expect(plan.Delete).To(HaveLen(1))
			Expect(plan.Update).To(HaveLen(0))
			Expect(plan.Objects).To(HaveLen(0))

			By("creating the cm", func() {
				err := k8sClient.Create(context.Background(), cm)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() {
					_ = k8sClient.Delete(context.Background(), cm)
				})
				plan, err = dsClient.Plan(ctx, "default", "set123", cm)
				Expect(err).ToNot(HaveOccurred())

				Expect(plan.Create).To(HaveLen(1))
				Expect(plan.Delete).To(HaveLen(1))
				Expect(plan.Update).To(HaveLen(0))
				Expect(plan.Objects).To(HaveLen(0))
			})

			By("modifying the desired state", func() {
				cm.Data = map[string]string{"test": "test"}

				plan, err = dsClient.Plan(ctx, "default", "set123", cm)
				Expect(err).ToNot(HaveOccurred())

				Expect(plan.Create).To(HaveLen(1))
				Expect(plan.Delete).To(HaveLen(1))
				Expect(plan.Update).To(HaveLen(0))
				Expect(plan.Objects).To(HaveLen(0))
			})
		})
	})
})

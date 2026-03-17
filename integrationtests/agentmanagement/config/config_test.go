package config_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("ConfigReconciler", func() {
	var cm *corev1.ConfigMap

	BeforeEach(func() {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.ManagerConfigName,
				Namespace: systemNamespace,
			},
		}
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, cm)
	})

	It("loads config when ConfigMap is created", func() {
		data, err := json.Marshal(config.Config{
			AgentImage: "rancher/fleet-agent:test",
		})
		Expect(err).NotTo(HaveOccurred())

		cm.Data = map[string]string{config.Key: string(data)}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(config.Get().AgentImage).To(Equal("rancher/fleet-agent:test"))
		}).Should(Succeed())
	})

	It("reloads config when ConfigMap is updated", func() {
		data, err := json.Marshal(config.Config{
			AgentImage: "rancher/fleet-agent:v1",
		})
		Expect(err).NotTo(HaveOccurred())

		cm.Data = map[string]string{config.Key: string(data)}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(config.Get().AgentImage).To(Equal("rancher/fleet-agent:v1"))
		}).Should(Succeed())

		// Update the ConfigMap to a new value
		data, err = json.Marshal(config.Config{
			AgentImage: "rancher/fleet-agent:v2",
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Namespace: systemNamespace,
			Name:      config.ManagerConfigName,
		}, cm)).To(Succeed())

		cm.Data = map[string]string{config.Key: string(data)}
		Expect(k8sClient.Update(ctx, cm)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(config.Get().AgentImage).To(Equal("rancher/fleet-agent:v2"))
		}).Should(Succeed())
	})
})

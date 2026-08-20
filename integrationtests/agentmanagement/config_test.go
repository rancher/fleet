package agentmanagement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/config"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("config ConfigMap watch", Ordered, func() {
	key := types.NamespacedName{Namespace: systemNamespace, Name: config.ManagerConfigName}

	BeforeAll(func() {
		DeferCleanup(func() {
			cm := &corev1.ConfigMap{}
			defaultCM := newConfigMap(systemNamespace, config.ManagerConfigName, config.DefaultConfig())
			err := k8sClient.Get(ctx, key, cm)
			switch {
			case apierrors.IsNotFound(err):
				Expect(k8sClient.Create(ctx, defaultCM)).To(Succeed())
			case err == nil:
				cm.Data = defaultCM.Data
				Expect(k8sClient.Update(ctx, cm)).To(Succeed())
			default:
				Expect(err).NotTo(HaveOccurred())
			}

			Eventually(func(g Gomega) {
				g.Expect(config.Get()).To(Equal(config.DefaultConfig()))
			}).Should(Succeed())
		})
	})

	It("reflects a newly created manager ConfigMap in config.Get()", func() {
		cfg := config.DefaultConfig()
		cfg.AgentImage = "rancher/fleet-agent:config-test-created"
		cfg.IgnoreClusterRegistrationLabels = true

		Expect(k8sClient.Create(ctx, newConfigMap(systemNamespace, config.ManagerConfigName, cfg))).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(config.Get().AgentImage).To(Equal("rancher/fleet-agent:config-test-created"))
			g.Expect(config.Get().IgnoreClusterRegistrationLabels).To(BeTrue())
		}).Should(Succeed())
	})

	It("reflects an updated manager ConfigMap in config.Get()", func() {
		cfg := config.DefaultConfig()
		cfg.AgentImage = "rancher/fleet-agent:config-test-updated"
		cfg.IgnoreClusterRegistrationLabels = false

		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, key, cm)).To(Succeed())

		updated := newConfigMap(systemNamespace, config.ManagerConfigName, cfg)
		cm.Data = updated.Data
		Expect(k8sClient.Update(ctx, cm)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(config.Get().AgentImage).To(Equal("rancher/fleet-agent:config-test-updated"))
			g.Expect(config.Get().IgnoreClusterRegistrationLabels).To(BeFalse())
		}).Should(Succeed())
	})

	It("treats deletion of the manager ConfigMap as a no-op, retaining the last known config", func() {
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, key, cm)).To(Succeed())
		Expect(k8sClient.Delete(ctx, cm)).To(Succeed())

		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, key, &corev1.ConfigMap{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			g.Expect(config.Get().AgentImage).To(Equal("rancher/fleet-agent:config-test-updated"))
		}).Should(Succeed())
	})

	It("ignores a ConfigMap with the right name in the wrong namespace", func() {
		cfg := config.DefaultConfig()
		cfg.AgentImage = "rancher/fleet-agent:should-be-ignored-namespace"

		Expect(k8sClient.Create(ctx, newConfigMap("default", config.ManagerConfigName, cfg))).To(Succeed())

		Consistently(func(g Gomega) {
			g.Expect(config.Get().AgentImage).To(Equal("rancher/fleet-agent:config-test-updated"))
		}).Should(Succeed())
	})

	It("ignores a ConfigMap with the wrong name in the system namespace", func() {
		cfg := config.DefaultConfig()
		cfg.AgentImage = "rancher/fleet-agent:should-be-ignored-name"

		Expect(k8sClient.Create(ctx, newConfigMap(systemNamespace, "not-the-manager-config", cfg))).To(Succeed())

		Consistently(func(g Gomega) {
			g.Expect(config.Get().AgentImage).To(Equal("rancher/fleet-agent:config-test-updated"))
		}).Should(Succeed())
	})
})

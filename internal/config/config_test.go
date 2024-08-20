package config_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"

	"github.com/rancher/fleet/internal/config"
)

var _ = Describe("Config", func() {
	When("not having set a value for gitClientTimeout", func() {
		It("should return the default value", func() {
			cfg, err := config.ReadConfig(&v1.ConfigMap{Data: map[string]string{}})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.GitClientTimeout.Duration).To(Equal(30 * time.Second))
		})
	})
	When("having set a value for gitClientTimeout", func() {
		It("should return the set value", func() {
			jsonConfig := `{"gitClientTimeout": "20s"}`
			cfg, err := config.ReadConfig(&v1.ConfigMap{
				Data: map[string]string{
					"config": jsonConfig,
				},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.GitClientTimeout.Duration).To(Equal(20 * time.Second))
		})
	})
})

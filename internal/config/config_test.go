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

	When("a duration value is not a valid Go duration", func() {
		It("does not error and falls back to the default for that key", func() {
			cfg, err := config.ReadConfig(&v1.ConfigMap{Data: map[string]string{
				"config": `{"gitClientTimeout": "1d"}`,
			}})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.GitClientTimeout.Duration).To(Equal(30 * time.Second))
		})

		It("handles every duration key (agentCheckinInterval, garbageCollectionInterval)", func() {
			cfg, err := config.ReadConfig(&v1.ConfigMap{Data: map[string]string{
				"config": `{"agentCheckinInterval": "1w", "garbageCollectionInterval": "7d12h"}`,
			}})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.AgentCheckinInterval.Duration).To(Equal(time.Duration(0)))
			Expect(cfg.GarbageCollectionInterval.Duration).To(Equal(time.Duration(0)))
		})

		It("keeps valid keys and only defaults the invalid one", func() {
			cfg, err := config.ReadConfig(&v1.ConfigMap{Data: map[string]string{
				"config": `{"gitClientTimeout": "20s", "garbageCollectionInterval": "1d"}`,
			}})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.GitClientTimeout.Duration).To(Equal(20 * time.Second))
			Expect(cfg.GarbageCollectionInterval.Duration).To(Equal(time.Duration(0)))
		})

		It("preserves non-duration fields unaltered", func() {
			cfg, err := config.ReadConfig(&v1.ConfigMap{Data: map[string]string{
				"config": `{
					"agentImage": "my-registry/fleet-agent:v1.2.3",
					"apiServerURL": "https://kube.example.com:6443",
					"labels": {"env": "prod", "team": "platform"},
					"agentCheckinInterval": "bad",
					"gitClientTimeout": "45s"
				}`,
			}})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.AgentImage).To(Equal("my-registry/fleet-agent:v1.2.3"))
			Expect(cfg.APIServerURL).To(Equal("https://kube.example.com:6443"))
			Expect(cfg.Labels).To(Equal(map[string]string{"env": "prod", "team": "platform"}))
			Expect(cfg.AgentCheckinInterval.Duration).To(Equal(time.Duration(0)))
			Expect(cfg.GitClientTimeout.Duration).To(Equal(45 * time.Second))
		})
	})

	When("a duration value is a bare number", func() {
		It("treats 0 as the default sentinel without erroring", func() {
			cfg, err := config.ReadConfig(&v1.ConfigMap{Data: map[string]string{
				"config": `{"garbageCollectionInterval": 0}`,
			}})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.GarbageCollectionInterval.Duration).To(Equal(time.Duration(0)))
		})

		It("falls back to the default for a non-zero number", func() {
			cfg, err := config.ReadConfig(&v1.ConfigMap{Data: map[string]string{
				"config": `{"gitClientTimeout": 5}`,
			}})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.GitClientTimeout.Duration).To(Equal(30 * time.Second))
		})
	})

	When("the config is not a valid mapping at all", func() {
		It("still surfaces the unmarshal error", func() {
			cfg, err := config.ReadConfig(&v1.ConfigMap{Data: map[string]string{
				"config": "- a\n- b\n",
			}})
			Expect(err).To(HaveOccurred())
			Expect(cfg).To(Equal(config.DefaultConfig()))
		})
	})
})

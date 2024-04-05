package clusterregistration_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/internal/config"
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ClusterRegistration Controller Suite")
}

var _ = BeforeSuite(func() {
	_ = config.SetAndTrigger(&config.Config{IgnoreClusterRegistrationLabels: false})
})

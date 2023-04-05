// Package singlecluster contains e2e tests deploying to a single cluster. The tests use kubectl to apply manifests. Expectations are verified by checking cluster resources. Assets refer to the https://github.com/rancher/fleet-test-data git repo.
package singlecluster_test

import (
	"testing"

	"github.com/rancher/fleet/e2e/testenv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite for Single-Cluster")
}

var (
	env *testenv.Env
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	testenv.SetRoot("../..")

	env = testenv.New()
})

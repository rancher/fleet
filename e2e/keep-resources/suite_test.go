package examples_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(testenv.FailAndGather)
	RunSpecs(t, "E2E Suite for keepResources")
}

var (
	env *testenv.Env
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	testenv.SetRoot("../..")

	env = testenv.New()
})

package require_secrets

import (
	"testing"

	"github.com/rancher/fleet/e2e/testenv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite for Github Secrets based Examples")
}

var (
	env *testenv.Env
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	testenv.SetRoot("../..")

	env = testenv.New()
})

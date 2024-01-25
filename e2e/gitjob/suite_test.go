package e2e_test

import (
	"testing"

	"github.com/rancher/fleet/e2e/testenv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite for Git Repo Tests")
}

var (
	env *testenv.Env
)

var _ = BeforeSuite(func() {
	testenv.SetRoot("../..")

	env = testenv.New()

	out, err := env.Kubectl.Apply("-f", testenv.AssetPath("gitjob/rbac.yaml"))
	Expect(err).ToNot(HaveOccurred(), out)
})

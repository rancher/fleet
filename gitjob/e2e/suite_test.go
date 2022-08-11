package e2e_test

import (
	"testing"

	"github.com/rancher/gitjob/e2e/testenv"

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
	testenv.SetRoot("..")

	env = testenv.New()

	env.Kubectl.Apply("-f", testenv.AssetPath("rbac.yaml"))
})

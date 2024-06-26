// Package installation contains e2e tests deploying Fleet to multiple clusters. The tests use kubectl to apply
// manifests. Expectations are verified by checking cluster resources.
package installation_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Installation Suite for Multi-Cluster")
}

var (
	env    *testenv.Env
	ku     kubectl.Command
	config string
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	testenv.SetRoot("../..")

	env = testenv.New()
	ku = env.Kubectl.Context(env.Upstream)

	// Save initial state of `fleet-controller` config map
	cfg, err := ku.Get(
		"configmap",
		"fleet-controller",
		"-n",
		"cattle-fleet-system",
		"-o",
		"jsonpath={.data.config}")
	Expect(err).ToNot(HaveOccurred(), cfg)

	cfg = strings.ReplaceAll(cfg, `"`, `\"`)
	config = strings.ReplaceAll(cfg, "\n", "")
})

var _ = AfterSuite(func() {
	// Restore initial state of config map
	out, err := ku.Patch(
		"configmap",
		"fleet-controller",
		"-n",
		"cattle-fleet-system",
		"--type=merge",
		"-p",
		fmt.Sprintf(`{"data":{"config":"%s"}}`, config),
	)
	Expect(err).ToNot(HaveOccurred(), string(out))
})

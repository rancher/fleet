// Package multicluster contains e2e tests deploying to multiple clusters. The tests use kubectl to apply manifests. Expectations are verified by checking cluster resources. Assets refer to the https://github.com/rancher/fleet-test-data git repo.
package multicluster_test

import (
	"os"
	"strings"
	"testing"

	"github.com/rancher/fleet/e2e/testenv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(testenv.FailAndGather)
	RunSpecs(t, "E2E Suite for Multi-Cluster")
}

var (
	env                  *testenv.Env
	dsCluster            = "second" // name of the Fleet Cluster resource
	k3dDownstreamCluster = "downstream"
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	testenv.SetRoot("../..")

	env = testenv.New()

	if dsClusterEnvVar := os.Getenv("CI_REGISTERED_CLUSTER"); dsClusterEnvVar != "" {
		dsCluster = dsClusterEnvVar
	}

	if k3dDSClusterEnvVar := os.Getenv("FLEET_E2E_CLUSTER_DOWNSTREAM"); k3dDSClusterEnvVar != "" {
		k3dDownstreamCluster = strings.TrimPrefix(k3dDSClusterEnvVar, "k3d-")
	}
})

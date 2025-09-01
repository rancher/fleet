// Package testenv contains common helpers for tests
package testenv

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

const (
	Timeout       = 5 * time.Minute
	ShortTimeout  = "5s"
	MediumTimeout = "120s"
)

type Env struct {
	Kubectl kubectl.Command
	// Upstream context for cluster containing the fleet controller and local agent
	Upstream string
	// Downstream context for fleet-agent cluster
	Downstream string
	// Managed downstream cluster
	ManagedDownstream string
	// Namespace which contains the cluster resource for most E2E tests
	// (cluster registration namespace)
	Namespace string
	// Namespace which contains downstream clusters, besides the local
	// cluster, for some multi-cluster E2E tests
	ClusterRegistrationNamespace string
}

func New() *Env {
	env := &Env{
		Kubectl:                      kubectl.New("", "default"),
		Upstream:                     "k3d-upstream",
		Downstream:                   "k3d-downstream",
		ManagedDownstream:            "k3d-managed-downstream",
		Namespace:                    "fleet-local",
		ClusterRegistrationNamespace: "fleet-default",
	}
	env.getShellEnv()
	return env
}

func (e *Env) getShellEnv() {
	if val := os.Getenv("FLEET_E2E_CLUSTER"); val != "" {
		e.Upstream = val
	}
	if val := os.Getenv("FLEET_E2E_CLUSTER_DOWNSTREAM"); val != "" {
		e.Downstream = val
	}
	if val := os.Getenv("FLEET_E2E_NS"); val != "" {
		e.Namespace = val
	}
	if val := os.Getenv("FLEET_E2E_NS_DOWNSTREAM"); val != "" {
		e.ClusterRegistrationNamespace = val
	}
}

// NewNamespaceName returns a name for a namespace that is unique to the test
// run. e.g. as a targetNamespace for workloads
func NewNamespaceName(name string, s rand.Source) string {
	p := make([]byte, 12)
	r := rand.New(s) // nolint:gosec // non-crypto usage
	_, err := r.Read(p)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("test-%.20s-%.12s", name, hex.EncodeToString(p))
}

// AddRandomSuffix adds a random suffix to a given name.
func AddRandomSuffix(name string, s rand.Source) string {
	p := make([]byte, 6)
	r := rand.New(s) // nolint:gosec // non-crypto usage
	_, err := r.Read(p)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s-%s", name, hex.EncodeToString(p))
}

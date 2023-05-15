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

const Timeout = 5 * time.Minute

type Env struct {
	Kubectl kubectl.Command
	// Upstream context for cluster containing the fleet controller and local agent
	Upstream string
	// Downstream context for fleet-agent cluster
	Downstream string
	// Namespace which contains the cluster resource (cluster registration namespace)
	Namespace string
}

func New() *Env {
	env := &Env{
		Kubectl:    kubectl.New("", "default"),
		Upstream:   "k3d-upstream",
		Downstream: "k3d-downstream",
		Namespace:  "fleet-default",
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
}

// NewNamespaceName returns a name for a namespace that is unique to the test
// run. e.g. as a targetNamespace for workloads
func NewNamespaceName(name string) string {
	rand.Seed(time.Now().UnixNano())
	p := make([]byte, 12)
	rand.Read(p) // nolint:gosec // Non-crypto use
	return fmt.Sprintf("test-%.20s-%.12s", name, hex.EncodeToString(p))
}

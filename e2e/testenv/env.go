// Package testenv contains common helpers for tests
package testenv

import (
	"os"
	"time"

	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

const Timeout = 5 * time.Minute

type Env struct {
	Kubectl kubectl.Command
	// Upstream context for cluster containing the fleet controller and local agent
	Upstream   string
	Downstream string
	Namespace  string
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

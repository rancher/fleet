// Package testenv contains common helpers for tests
package testenv

import (
	"os"
	"time"

	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

const Timeout = 5 * time.Minute

type Env struct {
	Kubectl    kubectl.Command
	Fleet      string
	Downstream string
	Namespace  string
}

func New() *Env {
	env := &Env{
		Kubectl:    kubectl.New("", "default"),
		Fleet:      "k3d-k3s-default",
		Downstream: "k3d-k3s-second",
		Namespace:  "fleet-default",
	}
	env.getShellEnv()
	return env
}

func (e *Env) getShellEnv() {
	if val := os.Getenv("FLEET_E2E_CLUSTER"); val != "" {
		e.Fleet = val
	}
	if val := os.Getenv("FLEET_E2E_CLUSTER_DOWNSTREAM"); val != "" {
		e.Downstream = val
	}
	if val := os.Getenv("FLEET_E2E_NS"); val != "" {
		e.Namespace = val
	}
}

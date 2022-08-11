// Package testenv contains common helpers for tests
package testenv

import (
	"time"

	"github.com/rancher/gitjob/e2e/testenv/kubectl"
)

const Timeout = 5 * time.Minute

type Env struct {
	Kubectl kubectl.Command
}

func New() *Env {
	return &Env{
		Kubectl: kubectl.New("", "default"),
	}
}

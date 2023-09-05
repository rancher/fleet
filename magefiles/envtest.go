//go:build mage

package main

import (
	"errors"
	"os"
	"path"

	"github.com/magefile/mage/sh"
)

const (
	envtestK8sVersion = "1.27.1"
)

func EnvTest() error {
	localbin, err := findOrCreateLocalBin()
	if err != nil {
		return err
	}

	version := envtestK8sVersion
	if v, found := os.LookupEnv("ENVTEST_K8S_VERSION"); found {
		version = v
	}

	cmd := path.Join(localbin, "envtest")
	if _, err := os.Stat(cmd); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}

	env := map[string]string{"GOBIN": localbin}
	return sh.RunWithV(env,
		"go", "install", "sigs.k8s.io/controller-tools/cmd/controller-gen@"+version,
	)
}

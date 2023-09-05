//go:build mage

package main

import (
	"os"
	"path"

	"github.com/magefile/mage/sh"
)

const (
	controllerGenVersion = "v0.12.0"
)

func Generate() error {
	localbin := lookupLocalBin()
	cmd := path.Join(localbin, "controller-gen")
	if !exists(cmd) {
		err := ControllerGen()
		if err != nil {
			return err
		}
	}

	return sh.RunV(cmd, `object:headerFile="dev/boilerplate.go.txt"`, "paths=./pkg/apis/...")
}

func Manifests() error {
	localbin := lookupLocalBin()
	cmd := path.Join(localbin, "controller-gen")

	if !exists(cmd) {
		err := ControllerGen()
		if err != nil {
			return err
		}
	}
	return sh.RunV(cmd,
		"rbac:roleName=manager-role",
		"crd",
		"paths=./pkg/apis/...",
		"output:crd:artifacts:config=docs/crd",
	)
}

func CRD() error {
	localbin := lookupLocalBin()
	cmd := path.Join(localbin, "controller-gen")

	if !exists(cmd) {
		err := ControllerGen()
		if err != nil {
			return err
		}
	}

	_ = os.RemoveAll("./docs/crd")
	_ = sh.Run(cmd,
		"crd:generateEmbeddedObjectMeta=true,allowDangerousTypes=false",
		"paths=./pkg/apis/...",
		"output:crd:dir=./docs/crd",
	)

	out, err := os.OpenFile("charts/fleet-crd/templates/crds.yaml", os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	// maintain original order
	for _, name := range []string{"bundles", "bundledeployments", "bundlenamespacemappings", "clustergroups", "clusters", "clusterregistrationtokens", "gitrepos", "clusterregistrations", "gitreporestrictions", "contents", "imagescans"} {

		b, err := os.ReadFile("./docs/crd/fleet.cattle.io_" + name + ".yaml")
		if err != nil {
			return err
		}

		_, err = out.Write(b)
		if err != nil {
			return err
		}
	}
	return nil

}

func ControllerGen() error {
	localbin, err := findOrCreateLocalBin()
	if err != nil {
		return err
	}

	version := controllerGenVersion
	if v, found := os.LookupEnv("CONTROLLER_TOOLS_VERSION"); found {
		version = v
	}

	cmd := path.Join(localbin, "controller-gen")
	if out, err := sh.Output(cmd, "--version"); out != version || err != nil {
		env := map[string]string{"GOBIN": localbin}
		return sh.RunWithV(env,
			"go", "install", "sigs.k8s.io/controller-tools/cmd/controller-gen@"+version,
		)
	}
	return nil
}

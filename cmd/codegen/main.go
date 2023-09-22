package main

import (
	"fmt"
	"os"

	"github.com/rancher/fleet/pkg/crd"
	controllergen "github.com/rancher/wrangler/pkg/controller-gen"
	"github.com/rancher/wrangler/pkg/controller-gen/args"

	// Ensure gvk gets loaded in wrangler/pkg/gvk cache
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apiextensions.k8s.io/v1"

	// To keep the dependency in go.mod
	_ "sigs.k8s.io/controller-tools/pkg/crd"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "crds" {
		fmt.Println("Writing CRDs to", os.Args[2])
		if err := crd.WriteFile(os.Args[2]); err != nil {
			panic(err)
		}
		return
	}

	os.Unsetenv("GOPATH")
	controllergen.Run(args.Options{
		OutputPackage: "github.com/rancher/fleet/pkg/generated",
		Boilerplate:   "scripts/boilerplate.go.txt",
		Groups: map[string]args.Group{
			"fleet.cattle.io": {
				Types: []interface{}{
					"./pkg/apis/fleet.cattle.io/v1alpha1",
				},
				GenerateTypes: true,
			},
		},
	})
}

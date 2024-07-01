package main

import (
	"os"

	controllergen "github.com/rancher/wrangler/v3/pkg/controller-gen"
	"github.com/rancher/wrangler/v3/pkg/controller-gen/args"

	// Ensure gvk gets loaded in wrangler/pkg/gvk cache
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/apiextensions.k8s.io/v1"

	// To keep the dependency in go.mod
	_ "sigs.k8s.io/controller-tools/pkg/crd"
)

func main() {
	os.Unsetenv("GOPATH")
	controllergen.Run(args.Options{
		OutputPackage: "github.com/rancher/fleet/pkg/generated",
		Boilerplate:   "cmd/codegen/boilerplate.go.txt",
		Groups: map[string]args.Group{
			"fleet.cattle.io": {
				Types: []interface{}{
					"./pkg/apis/fleet.cattle.io/v1alpha1",
				},
			},
		},
	})
}

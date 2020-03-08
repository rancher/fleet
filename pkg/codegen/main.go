package main

import (
	"os"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	controllergen "github.com/rancher/wrangler/pkg/controller-gen"
	"github.com/rancher/wrangler/pkg/controller-gen/args"
)

func main() {
	os.Unsetenv("GOPATH")
	controllergen.Run(args.Options{
		OutputPackage: "github.com/rancher/fleet/pkg/generated",
		Boilerplate:   "scripts/boilerplate.go.txt",
		Groups: map[string]args.Group{
			"fleet.cattle.io": {
				Types: []interface{}{
					fleet.Bundle{},
					fleet.BundleDeployment{},
					fleet.ClusterGroup{},
					fleet.Cluster{},
					fleet.ClusterGroupToken{},
					fleet.ClusterRegistrationRequest{},
					fleet.Content{},
				},
				GenerateTypes: true,
			},
		},
	})
}

package main

import (
	"os"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	controllergen "github.com/rancher/wrangler/pkg/controller-gen"
	"github.com/rancher/wrangler/pkg/controller-gen/args"
)

func main() {
	os.Unsetenv("GOPATH")
	controllergen.Run(args.Options{
		OutputPackage: "github.com/rancher/gitjob/pkg/generated",
		Boilerplate:   "scripts/boilerplate.go.txt",
		Groups: map[string]args.Group{
			"gitjob.cattle.io": {
				Types: []interface{}{
					v1.GitJob{},
				},
				GenerateTypes: true,
			},
		},
	})
}

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/invopop/jsonschema"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

type AllCRDs struct {
	GitRepo   v1alpha1.GitRepo     `json:"gitrepo"`
	Cluster   v1alpha1.Cluster     `json:"cluster"`
	Bundle    v1alpha1.Bundle      `json:"bundle"`
	FleetYAML v1alpha1.FleetYAML   `json:"fleetYaml"`
	HelmOp    v1alpha1.HelmOptions `json:"helmOptions"`
}

func main() {
	reflector := jsonschema.Reflector{
		DoNotReference: true, // optional: inline definitions
	}

	schema := reflector.Reflect(new(AllCRDs))

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		panic(err)
	}

	err = os.WriteFile("fleet-combined.schema.json", data, 0644)
	if err != nil {
		panic(err)
	}

	fmt.Println("Schema written to fleet-combined.schema.json")
}

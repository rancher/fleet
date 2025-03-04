package helmvalues

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// SetValues sets the values in the bundle from the data map. It mutates the bundle.
func SetValues(bundle *fleet.Bundle, data map[string][]byte) error {
	if v, ok := data["values.yaml"]; ok && string(v) != "" {
		gm := fleet.GenericMap{}
		if err := gm.UnmarshalJSON(v); err != nil {
			return fmt.Errorf("failed to unmarshal values.yaml: %w", err)
		}
		if bundle.Spec.Helm == nil {
			bundle.Spec.Helm = &fleet.HelmOptions{}
		}
		bundle.Spec.Helm.Values = &gm
	}

	for i, target := range bundle.Spec.Targets {
		if v, ok := data[target.Name]; ok && string(v) != "" {
			gm := fleet.GenericMap{}
			if err := gm.UnmarshalJSON(v); err != nil {
				return fmt.Errorf("failed to unmarshal values for target %q: %w", target.Name, err)
			}
			if bundle.Spec.Targets[i].Helm == nil {
				bundle.Spec.Targets[i].Helm = &fleet.HelmOptions{}
			}
			bundle.Spec.Targets[i].Helm.Values = &gm
		}
	}

	return nil
}

// SetOptions sets the values in the options of the bundle deployment from the
// data map. It mutates the bundle deployment.
// It sets the staged options, however they are not used by the agent.
func SetOptions(bd *fleet.BundleDeployment, data map[string][]byte) error {
	if v, ok := data[ValuesKey]; ok && string(v) != "" {
		gm := fleet.GenericMap{}
		if err := gm.UnmarshalJSON(v); err != nil {
			return fmt.Errorf("failed to unmarshal values: %w", err)
		}
		if bd.Spec.Options.Helm == nil {
			bd.Spec.Options.Helm = &fleet.HelmOptions{}
		}
		bd.Spec.Options.Helm.Values = &gm
	}

	if v, ok := data[StagedValuesKey]; ok && string(v) != "" {
		gm := fleet.GenericMap{}
		if err := gm.UnmarshalJSON(v); err != nil {
			return fmt.Errorf("failed to unmarshal values: %w", err)
		}
		if bd.Spec.StagedOptions.Helm == nil {
			bd.Spec.StagedOptions.Helm = &fleet.HelmOptions{}
		}
		bd.Spec.StagedOptions.Helm.Values = &gm
	}

	return nil
}

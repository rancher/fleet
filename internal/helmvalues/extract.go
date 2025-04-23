package helmvalues

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// ExtractOptions extracts the values from options in a bundle deployment.
func ExtractOptions(bd *fleet.BundleDeployment) (string, []byte, []byte, error) {
	var options []byte
	if bd.Spec.Options.Helm != nil && bd.Spec.Options.Helm.Values != nil {
		var err error
		options, err = bd.Spec.Options.Helm.Values.MarshalJSON()
		if err != nil {
			err = fmt.Errorf("failed to marshal values: %w", err)
			return "", []byte{}, []byte{}, err
		}
		if string(options) == "null" || string(options) == "{}" {
			options = []byte{}
		}
	}

	var staged []byte
	if bd.Spec.StagedOptions.Helm != nil && bd.Spec.StagedOptions.Helm.Values != nil {
		var err error
		staged, err = bd.Spec.StagedOptions.Helm.Values.MarshalJSON()
		if err != nil {
			err = fmt.Errorf("failed to marshal staged values: %w", err)
			return "", []byte{}, []byte{}, err
		}
		if string(staged) == "null" || string(staged) == "{}" {
			staged = []byte{}
		}
	}

	var hash string
	if len(options) > 0 || len(staged) > 0 {
		hash = HashOptions(options, staged)
	}

	return hash, options, staged, nil
}

// ClearOptions removes values from the new bundle deployment
func ClearOptions(bd *fleet.BundleDeployment) {
	if bd.Spec.Options.Helm != nil {
		bd.Spec.Options.Helm.Values = nil
	}
	if bd.Spec.StagedOptions.Helm != nil {
		bd.Spec.StagedOptions.Helm.Values = nil
	}
}

// ExtractValues extracts the values from the bundle and returns the values and
// a hash.
func ExtractValues(bundle *fleet.Bundle) (string, map[string][]byte, error) {
	data := map[string][]byte{}
	spec := bundle.Spec

	if spec.Helm != nil && spec.Helm.Values != nil && len(spec.Helm.Values.Data) > 0 {
		v, err := spec.Helm.Values.MarshalJSON()
		if err != nil {
			return "", data, err
		}
		data["values.yaml"] = v
	}

	for _, target := range spec.Targets {
		if target.Helm == nil || target.Helm.Values == nil || len(target.Helm.Values.Data) == 0 {
			continue
		}

		if target.Name == "" {
			return "", data, fmt.Errorf("target name is required")
		}

		v, err := target.Helm.Values.MarshalJSON()
		if err != nil {
			return "", data, err
		}
		data[target.Name] = v
	}

	if len(data) == 0 {
		return "", data, nil
	}

	// Note: we assume json.Marshal is stable for maps
	hash, err := HashValuesSecret(data)
	if err != nil {
		return "", data, err
	}

	return hash, data, nil
}

// ClearValues removes the values from a bundle. It mutates the bundle.
func ClearValues(bundle *fleet.Bundle) {
	if bundle.Spec.Helm != nil {
		bundle.Spec.Helm.Values = nil
		bundle.Spec.Helm.ValuesFiles = nil
	}
	for i := range bundle.Spec.Targets {
		if bundle.Spec.Targets[i].Helm == nil {
			continue
		}
		bundle.Spec.Targets[i].Helm.Values = nil
		bundle.Spec.Targets[i].Helm.ValuesFiles = nil
	}
}

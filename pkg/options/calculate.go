package options

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/overlay"
	"github.com/rancher/wrangler/pkg/data"
)

func DeploymentID(manifest *manifest.Manifest, opts fleet.BundleDeploymentOptions) (string, error) {
	_, digest, err := manifest.Content()
	if err != nil {
		return "", err
	}

	h := sha256.New()
	if err := json.NewEncoder(h).Encode(&opts); err != nil {
		return "", err
	}

	return digest + ":" + hex.EncodeToString(h.Sum(nil)), nil
}

func Calculate(spec *fleet.BundleSpec, target *fleet.BundleTarget) (fleet.BundleDeploymentOptions, error) {
	result := spec.BundleDeploymentOptions

	allOverlays, overlays, err := overlay.Resolve(spec, target.Overlays...)
	if err != nil {
		return result, err
	}

	for _, overlay := range overlays {
		result = merge(result, allOverlays[overlay].BundleDeploymentOptions)
	}

	return merge(result, target.BundleDeploymentOptions), nil
}

func merge(base, next fleet.BundleDeploymentOptions) fleet.BundleDeploymentOptions {
	if next.DefaultNamespace != "" {
		base.DefaultNamespace = next.DefaultNamespace
	} else if next.DefaultNamespace == "-" {
		base.DefaultNamespace = ""
	}
	if next.TimeoutSeconds > 0 {
		base.TimeoutSeconds = next.TimeoutSeconds
	} else if next.TimeoutSeconds < 0 {
		base.TimeoutSeconds = 0
	}
	if base.Values == nil {
		base.Values = next.Values
	} else if next.Values != nil {
		base.Values.Data = data.MergeMaps(base.Values.Data, next.Values.Data)
	}
	if next.KustomizeDir != "" {
		base.KustomizeDir = next.KustomizeDir
	}
	return base
}

package options

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
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

func Calculate(spec *fleet.BundleSpec, target *fleet.BundleTarget) fleet.BundleDeploymentOptions {
	result := spec.BundleDeploymentOptions
	return merge(result, target.BundleDeploymentOptions)
}

func merge(base, next fleet.BundleDeploymentOptions) fleet.BundleDeploymentOptions {
	if next.DefaultNamespace != "" {
		base.DefaultNamespace = next.DefaultNamespace
	} else if next.DefaultNamespace == "-" {
		base.DefaultNamespace = ""
	}
	if next.TargetNamespace != "" {
		base.TargetNamespace = next.TargetNamespace
	} else if next.TargetNamespace == "-" {
		base.TargetNamespace = ""
	}
	if next.ServiceAccount != "" {
		base.ServiceAccount = next.ServiceAccount
	} else if next.ServiceAccount == "-" {
		base.ServiceAccount = ""
	}
	if next.ServiceAccount != "" {
		base.ServiceAccount = next.ServiceAccount
	} else if next.ServiceAccount == "-" {
		base.ServiceAccount = ""
	}
	if next.Helm != nil {
		if base.Helm == nil {
			base.Helm = &fleet.HelmOptions{}
		}
		if next.Helm.TimeoutSeconds > 0 {
			base.Helm.TimeoutSeconds = next.Helm.TimeoutSeconds
		} else if next.Helm.TimeoutSeconds < 0 {
			base.Helm.TimeoutSeconds = 0
		}
		if base.Helm.Values == nil {
			base.Helm.Values = next.Helm.Values
		} else if next.Helm.Values != nil {
			base.Helm.Values.Data = data.MergeMaps(base.Helm.Values.Data, next.Helm.Values.Data)
		}
		if next.Helm.Chart != "" {
			base.Helm.Chart = next.Helm.Chart
		}
		if next.Helm.ReleaseName != "" {
			base.Helm.ReleaseName = next.Helm.ReleaseName
		}
		base.Helm.Force = base.Helm.Force || next.Helm.Force
		base.Helm.TakeOwnership = base.Helm.TakeOwnership || next.Helm.TakeOwnership
	}
	if next.Kustomize != nil {
		if base.Kustomize == nil {
			base.Kustomize = &fleet.KustomizeOptions{}
		}
		if next.Kustomize.Dir != "" {
			base.Kustomize.Dir = next.Kustomize.Dir
		}
	}
	if next.Diff != nil {
		if base.Diff == nil {
			base.Diff = &fleet.DiffOptions{}
		}
		base.Diff.ComparePatches = append(base.Diff.ComparePatches, next.Diff.ComparePatches...)
	}
	if next.YAML != nil {
		if base.YAML == nil {
			base.YAML = &fleet.YAMLOptions{}
		}
		base.YAML.Overlays = append(base.YAML.Overlays, next.YAML.Overlays...)
	}
	if next.ForceSyncBefore != nil {
		base.ForceSyncBefore = next.ForceSyncBefore
	}
	return base
}

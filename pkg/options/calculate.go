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
	return merge(spec.BundleDeploymentOptions, target.BundleDeploymentOptions)
}

func merge(base, next fleet.BundleDeploymentOptions) fleet.BundleDeploymentOptions {
	result := *base.DeepCopy()
	if next.DefaultNamespace != "" {
		result.DefaultNamespace = next.DefaultNamespace
	} else if next.DefaultNamespace == "-" {
		result.DefaultNamespace = ""
	}
	if next.TargetNamespace != "" {
		result.TargetNamespace = next.TargetNamespace
	} else if next.TargetNamespace == "-" {
		result.TargetNamespace = ""
	}
	if next.ServiceAccount != "" {
		result.ServiceAccount = next.ServiceAccount
	} else if next.ServiceAccount == "-" {
		result.ServiceAccount = ""
	}
	if next.ServiceAccount != "" {
		result.ServiceAccount = next.ServiceAccount
	} else if next.ServiceAccount == "-" {
		result.ServiceAccount = ""
	}
	if next.Helm != nil {
		if result.Helm == nil {
			result.Helm = &fleet.HelmOptions{}
		}
		if next.Helm.TimeoutSeconds > 0 {
			result.Helm.TimeoutSeconds = next.Helm.TimeoutSeconds
		} else if next.Helm.TimeoutSeconds < 0 {
			result.Helm.TimeoutSeconds = 0
		}
		if result.Helm.Values == nil {
			result.Helm.Values = next.Helm.Values
		} else if next.Helm.Values != nil {
			result.Helm.Values.Data = data.MergeMaps(result.Helm.Values.Data, next.Helm.Values.Data)
		}
		if next.Helm.ValuesFrom != nil {
			result.Helm.ValuesFrom = append(result.Helm.ValuesFrom, next.Helm.ValuesFrom...)
		}
		if next.Helm.Repo != "" {
			result.Helm.Repo = next.Helm.Repo
		}
		if next.Helm.Chart != "" {
			result.Helm.Chart = next.Helm.Chart
		}
		if next.Helm.Version != "" {
			result.Helm.Version = next.Helm.Version
		}
		if next.Helm.ReleaseName != "" {
			result.Helm.ReleaseName = next.Helm.ReleaseName
		}
		result.Helm.Force = result.Helm.Force || next.Helm.Force
		result.Helm.Atomic = result.Helm.Atomic || next.Helm.Atomic
		result.Helm.TakeOwnership = result.Helm.TakeOwnership || next.Helm.TakeOwnership
	}
	if next.Kustomize != nil {
		if result.Kustomize == nil {
			result.Kustomize = &fleet.KustomizeOptions{}
		}
		if next.Kustomize.Dir != "" {
			result.Kustomize.Dir = next.Kustomize.Dir
		}
	}
	if next.Diff != nil {
		if result.Diff == nil {
			result.Diff = &fleet.DiffOptions{}
		}
		result.Diff.ComparePatches = append(result.Diff.ComparePatches, next.Diff.ComparePatches...)
	}
	if next.YAML != nil {
		if result.YAML == nil {
			result.YAML = &fleet.YAMLOptions{}
		}
		result.YAML.Overlays = append(result.YAML.Overlays, next.YAML.Overlays...)
	}
	if next.ForceSyncGeneration > 0 {
		result.ForceSyncGeneration = next.ForceSyncGeneration
	}
	return result
}

// Package options merges the BundleDeploymentOptions
package options

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/wrangler/pkg/data"
)

// DeploymentID hashes the options to a string
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

// Merge overrides the 'base' options with the 'target customization' options, if 'custom' is present (pure function)
func Merge(base, custom fleet.BundleDeploymentOptions) fleet.BundleDeploymentOptions { // nolint: gocyclo // business logic
	result := *base.DeepCopy()
	if custom.DefaultNamespace != "" {
		result.DefaultNamespace = custom.DefaultNamespace
	} else if custom.DefaultNamespace == "-" {
		result.DefaultNamespace = ""
	}
	if custom.TargetNamespace != "" {
		result.TargetNamespace = custom.TargetNamespace
	} else if custom.TargetNamespace == "-" {
		result.TargetNamespace = ""
	}
	if custom.ServiceAccount != "" {
		result.ServiceAccount = custom.ServiceAccount
	} else if custom.ServiceAccount == "-" {
		result.ServiceAccount = ""
	}
	if custom.ServiceAccount != "" {
		result.ServiceAccount = custom.ServiceAccount
	} else if custom.ServiceAccount == "-" {
		result.ServiceAccount = ""
	}
	if custom.Helm != nil {
		if result.Helm == nil {
			result.Helm = &fleet.HelmOptions{}
		}
		if custom.Helm.TimeoutSeconds > 0 {
			result.Helm.TimeoutSeconds = custom.Helm.TimeoutSeconds
		} else if custom.Helm.TimeoutSeconds < 0 {
			result.Helm.TimeoutSeconds = 0
		}
		if result.Helm.Values == nil {
			result.Helm.Values = custom.Helm.Values
		} else if custom.Helm.Values != nil {
			result.Helm.Values.Data = data.MergeMaps(result.Helm.Values.Data, custom.Helm.Values.Data)
		}
		if custom.Helm.ValuesFrom != nil {
			result.Helm.ValuesFrom = append(result.Helm.ValuesFrom, custom.Helm.ValuesFrom...)
		}
		if custom.Helm.Repo != "" {
			result.Helm.Repo = custom.Helm.Repo
		}
		if custom.Helm.Chart != "" {
			result.Helm.Chart = custom.Helm.Chart
		}
		if custom.Helm.Version != "" {
			result.Helm.Version = custom.Helm.Version
		}
		if custom.Helm.ReleaseName != "" {
			result.Helm.ReleaseName = custom.Helm.ReleaseName
		}
		result.Helm.Force = result.Helm.Force || custom.Helm.Force
		result.Helm.Atomic = result.Helm.Atomic || custom.Helm.Atomic
		result.Helm.TakeOwnership = result.Helm.TakeOwnership || custom.Helm.TakeOwnership
		result.Helm.DisablePreProcess = result.Helm.DisablePreProcess || custom.Helm.DisablePreProcess
		result.Helm.WaitForJobs = result.Helm.WaitForJobs || custom.Helm.WaitForJobs
	}
	if custom.Kustomize != nil {
		if result.Kustomize == nil {
			result.Kustomize = &fleet.KustomizeOptions{}
		}
		if custom.Kustomize.Dir != "" {
			result.Kustomize.Dir = custom.Kustomize.Dir
		}
	}
	if custom.Diff != nil {
		if result.Diff == nil {
			result.Diff = &fleet.DiffOptions{}
		}
		result.Diff.ComparePatches = append(result.Diff.ComparePatches, custom.Diff.ComparePatches...)
	}
	if custom.YAML != nil {
		if result.YAML == nil {
			result.YAML = &fleet.YAMLOptions{}
		}
		result.YAML.Overlays = append(result.YAML.Overlays, custom.YAML.Overlays...)
	}
	if custom.ForceSyncGeneration > 0 {
		result.ForceSyncGeneration = custom.ForceSyncGeneration
	}
	result.KeepResources = result.KeepResources || custom.KeepResources

	return result
}

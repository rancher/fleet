// Package options merges the BundleDeploymentOptions, so that targetCustomizations take effect.
package options

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"

	"github.com/rancher/fleet/internal/merge"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/data"
)

// mergeUnique merges two slices by key, with custom entries taking precedence
// over base entries for the same key. See merge.ByKey for full semantics.
func mergeUnique[T any](base, custom []T, keyFn func(T) string) []T {
	return merge.ByKey(base, custom, keyFn, func(_ T, c T) T { return c })
}

func downstreamResourceKey(r fleet.DownstreamResource) string {
	return strings.ToLower(r.Kind) + "|" + r.Name
}

func valuesFromKey(v fleet.ValuesFrom) string {
	if v.ConfigMapKeyRef != nil {
		return "configmap|" + v.ConfigMapKeyRef.Namespace + "|" + v.ConfigMapKeyRef.Name + "|" + v.ConfigMapKeyRef.Key
	}
	if v.SecretKeyRef != nil {
		return "secret|" + v.SecretKeyRef.Namespace + "|" + v.SecretKeyRef.Name + "|" + v.SecretKeyRef.Key
	}
	return "" // no stable identity; always append
}

func comparePatchKey(p fleet.ComparePatch) string {
	return p.APIVersion + "|" + p.Kind + "|" + p.Namespace + "|" + p.Name
}

// DeploymentID hashes the options to a string
func DeploymentID(manifestID string, opts fleet.BundleDeploymentOptions) (string, error) {
	h := sha256.New()
	sanitized := *opts.DeepCopy()
	// Diff-only changes should not trigger a new DeploymentID; they only affect drift detection.
	sanitized.Diff = nil
	// CorrectDrift governs how drift is reconciled, not what is deployed and it
	// should not trigger a new DeploymentID.
	sanitized.CorrectDrift = nil
	if err := json.NewEncoder(h).Encode(&sanitized); err != nil {
		return "", err
	}

	return manifestID + ":" + hex.EncodeToString(h.Sum(nil)), nil
}

// Merge overrides the 'base' options with the 'target customization' options, if 'custom' is present (pure function)
func Merge(base, custom fleet.BundleDeploymentOptions) fleet.BundleDeploymentOptions { //nolint: gocyclo // business logic
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
		if result.Helm.TemplateValues == nil {
			result.Helm.TemplateValues = custom.Helm.TemplateValues
		} else if custom.Helm.TemplateValues != nil {
			maps.Copy(result.Helm.TemplateValues, custom.Helm.TemplateValues)
		}
		if len(custom.Helm.ValuesFrom) > 0 {
			result.Helm.ValuesFrom = mergeUnique(result.Helm.ValuesFrom, custom.Helm.ValuesFrom, valuesFromKey)
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
		result.Helm.DisableDNS = result.Helm.DisableDNS || custom.Helm.DisableDNS
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
		result.Diff.ComparePatches = mergeUnique(result.Diff.ComparePatches, custom.Diff.ComparePatches, comparePatchKey)
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
	if custom.CorrectDrift != nil {
		result.CorrectDrift = custom.CorrectDrift
	}
	if len(custom.DownstreamResources) > 0 {
		result.DownstreamResources = mergeUnique(result.DownstreamResources, custom.DownstreamResources, downstreamResourceKey)
	}
	// result is a deep copy of base, so writing into these maps does not mutate
	// the caller's inputs. Custom keys override base keys for the same name.
	if len(custom.NamespaceLabels) > 0 {
		if result.NamespaceLabels == nil {
			result.NamespaceLabels = map[string]string{}
		}
		maps.Copy(result.NamespaceLabels, custom.NamespaceLabels)
	}
	if len(custom.NamespaceAnnotations) > 0 {
		if result.NamespaceAnnotations == nil {
			result.NamespaceAnnotations = map[string]string{}
		}
		maps.Copy(result.NamespaceAnnotations, custom.NamespaceAnnotations)
	}

	return result
}

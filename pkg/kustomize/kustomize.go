package kustomize

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/content"
	"github.com/rancher/fleet/pkg/manifest"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
)

func Process(m *manifest.Manifest) (*manifest.Manifest, error) {
	fs, err := toFilesystem(m)
	if err != nil {
		return nil, err
	}

	if !fs.Exists("kustomize.yaml") {
		return m, nil
	}

	yamlContent, err := kustomize(fs)
	if err != nil {
		return nil, err
	}

	newManifest := &manifest.Manifest{
		Resources: []fleet.BundleResource{
			{
				Name:    "manifests.yaml",
				Content: string(yamlContent),
			},
		},
	}

	return newManifest, nil
}

func toFilesystem(m *manifest.Manifest) (filesys.FileSystem, error) {
	f := filesys.MakeEmptyDirInMemory()
	for _, resource := range m.Resources {
		data, err := content.Decode(resource.Content, resource.Encoding)
		if err != nil {
			return nil, err
		}
		if _, err := f.AddFile(resource.Name, data); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func kustomize(fs filesys.FileSystem) ([]byte, error) {
	pcfg, err := konfig.EnabledPluginConfig()
	if err != nil {
		return nil, err
	}
	kust := krusty.MakeKustomizer(fs, &krusty.Options{
		DoLegacyResourceSort: true,
		LoadRestrictions:     types.LoadRestrictionsRootOnly,
		PluginConfig:         pcfg,
	})
	resMap, err := kust.Run(".")
	if err != nil {
		return nil, err
	}
	return resMap.AsYaml()
}

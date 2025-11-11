package kustomize

import (
	"path/filepath"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/data/convert"
	"github.com/rancher/fleet/internal/content"
	"github.com/rancher/fleet/internal/manifest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

const (
	KustomizeYAML = "kustomization.yaml"
	ManifestsYAML = "fleet-manifests.yaml"
)

func Process(m *manifest.Manifest, content []byte, dir string) ([]runtime.Object, bool, error) {
	if dir == "" {
		dir = "."
	}

	fs, err := toFilesystem(m, dir, content)
	if err != nil {
		return nil, false, err
	}

	d := filepath.Join(dir, KustomizeYAML)
	if !fs.Exists(d) {
		return nil, false, nil
	}

	if len(content) > 0 {
		if err := modifyKustomize(fs, dir); err != nil {
			return nil, false, err
		}
	}

	objs, err := kustomize(fs, dir)
	return objs, true, err
}

func containsString(slice []string, item string) bool {
	for _, j := range slice {
		if j == item {
			return true
		}
	}
	return false
}

func modifyKustomize(f filesys.FileSystem, dir string) error {
	file := filepath.Join(dir, KustomizeYAML)
	fileBytes, err := f.ReadFile(file)
	if err != nil {
		return err
	}

	data := map[string]interface{}{}
	if err := yaml.Unmarshal(fileBytes, &data); err != nil {
		return err
	}

	resources := convert.ToStringSlice(data["resources"])
	if containsString(resources, ManifestsYAML) {
		return nil
	}

	data["resources"] = append([]string{ManifestsYAML}, resources...)
	fileBytes, err = yaml.Marshal(data)
	if err != nil {
		return err
	}

	return f.WriteFile(file, fileBytes)
}

func toFilesystem(m *manifest.Manifest, dir string, manifestsContent []byte) (filesys.FileSystem, error) {
	f := filesys.MakeEmptyDirInMemory()
	for _, resource := range m.Resources {
		if resource.Name == "" {
			continue
		}
		data, err := content.Decode(resource.Content, resource.Encoding)
		if err != nil {
			return nil, err
		}
		if _, err := f.AddFile(resource.Name, data); err != nil {
			return nil, err
		}
	}

	_, err := f.AddFile(filepath.Join(dir, ManifestsYAML), manifestsContent)
	return f, err
}

func kustomize(fs filesys.FileSystem, dir string) (result []runtime.Object, err error) {
	pcfg := types.DisabledPluginConfig()
	kust := krusty.MakeKustomizer(&krusty.Options{
		LoadRestrictions: types.LoadRestrictionsRootOnly,
		PluginConfig:     pcfg,
	})
	resMap, err := kust.Run(fs, dir)
	if err != nil {
		return nil, err
	}
	for _, m := range resMap.Resources() {
		mm, err := m.Map()
		if err != nil {
			return nil, err
		}
		result = append(result, &unstructured.Unstructured{
			Object: mm,
		})
	}
	return
}

package kustomize

import (
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/rancher/fleet/pkg/content"
	"github.com/rancher/fleet/pkg/manifest"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
)

func Process(m *manifest.Manifest, content []byte, dir string) ([]runtime.Object, bool, error) {
	if dir == "" {
		dir = "."
	}

	fs, err := toFilesystem(m, content)
	if err != nil {
		return nil, false, err
	}

	d := filepath.Join(dir, "kustomize.yaml")
	if !fs.Exists(d) {
		return nil, false, nil
	}

	objs, err := kustomize(fs, dir)
	return objs, true, err
}

func toFilesystem(m *manifest.Manifest, manifestContent []byte) (filesys.FileSystem, error) {
	f := filesys.MakeEmptyDirInMemory()
	for _, resource := range m.Resources {
		if !strings.HasPrefix(resource.Name, "kustomize/") {
			continue
		}
		name := strings.TrimPrefix(resource.Name, "kustomize/")
		data, err := content.Decode(resource.Content, resource.Encoding)
		if err != nil {
			return nil, err
		}
		if _, err := f.AddFile(name, data); err != nil {
			return nil, err
		}
	}
	_, err := f.AddFile("manifests.yaml", manifestContent)
	return f, err
}

func kustomize(fs filesys.FileSystem, dir string) (result []runtime.Object, err error) {
	pcfg := konfig.DisabledPluginConfig()
	kust := krusty.MakeKustomizer(fs, &krusty.Options{
		LoadRestrictions: types.LoadRestrictionsRootOnly,
		PluginConfig:     pcfg,
	})
	resMap, err := kust.Run(dir)
	if err != nil {
		return nil, err
	}
	for _, m := range resMap.Resources() {
		result = append(result, &unstructured.Unstructured{
			Object: m.Map(),
		})
	}
	return
}

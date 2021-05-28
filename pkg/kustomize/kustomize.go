package kustomize

import (
	"bytes"
	"path/filepath"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/fleet/pkg/content"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/wrangler/pkg/data/convert"
	"github.com/rancher/wrangler/pkg/slice"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/kustomize/v3/commands/build"
	"sigs.k8s.io/yaml"
)

const (
	KustomizeYAML = "kustomization.yaml"
	ManifestsYAML = "fleet-manifests.yaml"
)

func Process(m *manifest.Manifest, content []byte, options fleet.KustomizeOptions) ([]runtime.Object, bool, error) {
	dir := options.Dir
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

	objs, err := kustomize(fs, dir, options.BuildOptions)
	return objs, true, err
}

func modifyKustomize(f filesys.FileSystem, dir string) error {
	file := filepath.Join(dir, KustomizeYAML)
	fileBytes, err := f.ReadFile(file)
	if err != nil {
		return err
	}

	data := map[string]interface{}{}
	if err := yaml.Unmarshal(fileBytes, &data); err != nil {
		return nil
	}

	resources := convert.ToStringSlice(data["resources"])
	if slice.ContainsString(resources, ManifestsYAML) {
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

func kustomize(fs filesys.FileSystem, dir string, buildOptions string) (result []runtime.Object, err error) {
	buildOpts := strings.Split(buildOptions, " ")
	var o build.Options
	cmd := build.NewCmdBuildWithOptions("build", new(bytes.Buffer), &o)
	cmd.Flags().Parse(buildOpts)
	if err := o.Validate([]string{dir}); err != nil {
		return nil, err
	}
	resMap, err := o.RunBuildWithoutEmitter(fs)
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

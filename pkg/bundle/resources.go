package bundle

import (
	"bytes"
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/go-getter"
	"github.com/pkg/errors"
	"github.com/rancher/fleet/modules/cli/pkg/progress"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/content"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	ManifestsDir = "manifests"
	ChartDir     = "chart"
	Overlays     = "overlays"
)

func readOverlays(ctx context.Context, meta *bundleMeta, bundle *fleet.BundleSpec, compress bool, base string) (map[string][]fleet.BundleResource, error) {
	var directories []directory

	overlayDir := meta.Overlays
	if overlayDir == "" {
		overlayDir = Overlays
	}

	for _, overlay := range overlays(bundle) {
		directories = append(directories, directory{
			base: base,
			path: filepath.Join(overlayDir, overlay),
			key:  overlay,
		})
	}

	return readDirectories(ctx, compress, directories...)
}

func readResources(ctx context.Context, meta *bundleMeta, compress bool, base string) ([]fleet.BundleResource, error) {
	var directories []directory

	directories, err := addDirectory(directories, base, meta.Manifests, ManifestsDir)
	if err != nil {
		return nil, err
	}

	directories, err = addDirectory(directories, base, meta.Chart, ChartDir)
	if err != nil {
		return nil, err
	}

	resources, err := readDirectories(ctx, compress, directories...)
	if err != nil {
		return nil, err
	}

	result := stripChartPrefix(resources[ChartDir])
	result = append(result, resources[ManifestsDir]...)
	return result, nil
}

func stripChartPrefix(resources []fleet.BundleResource) []fleet.BundleResource {
	chart := ""
	for _, resource := range resources {
		if strings.HasSuffix(resource.Name, "Chart.yaml") {
			if chart == "" || len(resource.Name) < len(chart) {
				chart = resource.Name
			}
		}
	}

	if chart == "" || chart == filepath.Join(ChartDir, "Chart.yaml") {
		return resources
	}

	var (
		newResources []fleet.BundleResource
		prefix       = strings.TrimSuffix(chart, "Chart.yaml")
	)

	for _, resource := range resources {
		if !strings.HasPrefix(resource.Name, prefix) {
			return resources
		}
		newResources = append(newResources, fleet.BundleResource{
			Name:     filepath.Join(ChartDir, strings.TrimPrefix(resource.Name, prefix)),
			Content:  resource.Content,
			Encoding: resource.Encoding,
		})
	}

	return newResources
}

func addDirectory(directories []directory, base, customDir, defaultDir string) ([]directory, error) {
	if customDir == "" {
		if _, err := os.Stat(filepath.Join(base, defaultDir)); err != nil {
			return nil, nil
		}
		customDir = defaultDir
	}

	return append(directories, directory{
		prefix: defaultDir,
		base:   base,
		path:   customDir,
	}), nil
}

type directory struct {
	prefix string
	base   string
	path   string
	key    string
}

func readDirectories(ctx context.Context, compress bool, directories ...directory) (map[string][]fleet.BundleResource, error) {
	var (
		sem    = semaphore.NewWeighted(4)
		result = map[string][]fleet.BundleResource{}
		l      = sync.Mutex{}
		p      = progress.NewProgress()
	)
	defer p.Close()

	eg, ctx := errgroup.WithContext(ctx)

	for _, dir := range directories {
		if err := sem.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		dir := dir
		eg.Go(func() error {
			defer sem.Release(1)
			resources, err := readDirectory(ctx, p, compress, dir.prefix, dir.base, dir.path)
			if err != nil {
				return err
			}

			key := dir.key
			if key == "" {
				key = dir.path
			}

			l.Lock()
			result[key] = resources
			l.Unlock()
			return nil
		})
	}

	return result, eg.Wait()
}

func readDirectory(ctx context.Context, progress *progress.Progress, compress bool, prefix, base, name string) ([]fleet.BundleResource, error) {
	var resources []fleet.BundleResource

	files, err := readContent(ctx, progress, base, name)
	if err != nil {
		return nil, err
	}

	for k := range files {
		resources = append(resources, fleet.BundleResource{
			Name: k,
		})
	}

	for i, resource := range resources {
		data := files[resource.Name]
		if compress || hasZero(data) {
			content, err := content.Base64GZ(files[resource.Name])
			if err != nil {
				return nil, err
			}
			resources[i].Content = content
			resources[i].Encoding = "base64+gz"
		} else {
			resources[i].Content = string(data)
		}
		if prefix != "" {
			resources[i].Name = filepath.Join(prefix, resources[i].Name)
		}
	}

	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Name < resources[j].Name
	})

	return resources, nil
}

func hasZero(data []byte) bool {
	return bytes.ContainsRune(data, 0x0)
}

func readContent(ctx context.Context, progress *progress.Progress, base, name string) (map[string][]byte, error) {
	temp, err := ioutil.TempDir("", "fleet")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)

	temp = filepath.Join(temp, "content")

	base, err = filepath.Abs(base)
	if err != nil {
		return nil, err
	}

	c := getter.Client{
		Ctx:              ctx,
		Src:              name,
		Dst:              temp,
		Pwd:              base,
		Mode:             getter.ClientModeDir,
		ProgressListener: progress,
	}

	if err := c.Get(); err != nil {
		return nil, err
	}

	files := map[string][]byte{}

	// dereference link if possible
	if dest, err := os.Readlink(temp); err == nil {
		temp = dest
	}

	err = filepath.Walk(temp, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		name, err := filepath.Rel(temp, path)
		if err != nil {
			return err
		}

		content, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}

		files[name] = content
		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read %s relative to %s", name, base)
	}

	return files, nil
}

package bundle

import (
	"bytes"
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/hashicorp/go-getter"
	"github.com/pkg/errors"
	"github.com/rancher/fleet/modules/cli/pkg/progress"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/content"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func readBaseResources(ctx context.Context, appName, base string, name string) ([]fleet.BundleResource, error) {
	if name == "" {
		name = "manifests"
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			return nil, nil
		}
	}

	p := progress.NewProgress()
	defer p.Close()

	bundleOverlay, err := readOverlay(ctx, p, appName, base, name)
	if err != nil {
		return nil, err
	}
	return bundleOverlay.Resources, nil
}

func readOverlays(ctx context.Context, appName, base string, names ...string) (map[string]*fleet.BundleOverlay, error) {
	var (
		sem    = semaphore.NewWeighted(4)
		result = map[string]*fleet.BundleOverlay{}
		l      = sync.Mutex{}
		p      = progress.NewProgress()
	)
	defer p.Close()

	eg, ctx := errgroup.WithContext(ctx)

	for _, name := range names {
		if err := sem.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		name := name
		eg.Go(func() error {
			defer sem.Release(1)
			bundle, err := readOverlay(ctx, p, appName, base, name)
			if err != nil {
				return err
			}

			l.Lock()
			result[name] = bundle
			l.Unlock()
			return nil
		})
	}

	return result, eg.Wait()
}

func readOverlay(ctx context.Context, progress *progress.Progress, appName, base, name string) (*fleet.BundleOverlay, error) {
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

	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Name < resources[j].Name
	})

	for i, resource := range resources {
		data := files[resource.Name]
		if hasZero(data) {
			content, err := content.Base64GZ(files[resource.Name])
			if err != nil {
				return nil, err
			}
			resources[i].Content = content
			resources[i].Encoding = "base64+gz"
		} else {
			resources[i].Content = string(data)
		}
	}

	return &fleet.BundleOverlay{
		Name:      name,
		Resources: resources,
	}, nil
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

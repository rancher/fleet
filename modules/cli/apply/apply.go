package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundle"
	"github.com/rancher/wrangler/pkg/yaml"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Options struct {
	BundleFile   string
	Compress     bool
	BundleReader io.Reader
	Output       io.Writer
}

func Apply(ctx context.Context, client *client.Getter, baseDirs []string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	if len(baseDirs) == 0 {
		baseDirs = []string{"."}
	}

	for i, baseDir := range baseDirs {
		if i > 0 && opts.Output != nil {
			opts.Output.Write([]byte("\n---\n"))
		}
		if err := Dir(ctx, client, baseDir, opts); err != nil {
			return err
		}
	}

	return nil
}

func readBundle(ctx context.Context, baseDir string, opts *Options) (*bundle.Bundle, error) {
	if opts.BundleReader != nil {
		var bundleResource fleet.Bundle
		if err := json.NewDecoder(opts.BundleReader).Decode(&bundleResource); err != nil {
			return nil, err
		}
		return bundle.New(&bundleResource)
	}

	return bundle.Open(ctx, baseDir, opts.BundleFile, &bundle.Options{
		Compress: opts.Compress,
	})
}

func Dir(ctx context.Context, client *client.Getter, baseDir string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	bundle, err := readBundle(ctx, baseDir, opts)
	if err != nil {
		return err
	}

	def := bundle.Definition.DeepCopy()
	def.Namespace = client.Namespace

	b, err := yaml.Export(def)
	if err != nil {
		return err
	}

	if opts.Output == nil {
		err = save(client, def)
	} else {
		_, err = opts.Output.Write(b)
	}

	return err
}

func save(client *client.Getter, bundle *fleet.Bundle) error {
	c, err := client.Get()
	if err != nil {
		return err
	}

	obj, err := c.Fleet.Bundle().Get(bundle.Namespace, bundle.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.Fleet.Bundle().Create(bundle)
		if err == nil {
			fmt.Printf("%s/%s\n", obj.Namespace, obj.Name)
		}
		return err
	} else if err != nil {
		return err
	}

	obj.Spec = bundle.Spec
	obj.Annotations = mergeMap(obj.Annotations, bundle.Annotations)
	obj.Labels = mergeMap(obj.Labels, bundle.Labels)
	_, err = c.Fleet.Bundle().Update(obj)
	if err == nil {
		fmt.Printf("%s/%s\n", obj.Namespace, obj.Name)
	}
	return err
}

func mergeMap(a, b map[string]string) map[string]string {
	result := map[string]string{}
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

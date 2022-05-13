package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundle"
	"github.com/rancher/fleet/pkg/bundleyaml"
	name2 "github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	disallowedChars = regexp.MustCompile("[^a-zA-Z0-9]+")
	multiDash       = regexp.MustCompile("-+")
	ErrNoResources  = errors.New("no resources found to deploy")
)

type Options struct {
	BundleFile      string
	TargetsFile     string
	Compress        bool
	BundleReader    io.Reader
	Output          io.Writer
	ServiceAccount  string
	TargetNamespace string
	Paused          bool
	Labels          map[string]string
	SyncGeneration  int64
	Auth            bundle.Auth
}

func globDirs(baseDir string) (result []string, err error) {
	paths, err := filepath.Glob(baseDir)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if s, err := os.Stat(path); err == nil && s.IsDir() {
			result = append(result, path)
		}
	}
	return
}

func Apply(ctx context.Context, client *client.Getter, name string, baseDirs []string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	if len(baseDirs) == 0 {
		baseDirs = []string{"."}
	}

	foundBundle := false
	for i, baseDir := range baseDirs {
		matches, err := globDirs(baseDir)
		if err != nil {
			return fmt.Errorf("invalid path glob %s: %w", baseDir, err)
		}
		for _, baseDir := range matches {
			if i > 0 && opts.Output != nil {
				if _, err := opts.Output.Write([]byte("\n---\n")); err != nil {
					return err
				}
			}
			err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
				// always consider the root valid
				if baseDir != path {
					if !info.IsDir() {
						return nil
					}
					if !bundleyaml.FoundFleetYamlInDirectory(path) {
						return nil
					}
				}
				if err := Dir(ctx, client, name, path, opts); err == ErrNoResources {
					logrus.Warnf("%s: %v", path, err)
					return nil
				} else if err != nil {
					return err
				}
				foundBundle = true
				return nil
			})
			if err != nil {
				return err
			}
		}
	}

	if !foundBundle {
		return fmt.Errorf("no resource found at the following paths to deploy: %v", baseDirs)
	}

	return nil
}

func createName(name, baseDir string) string {
	path := strings.ToLower(filepath.Join(name, baseDir))
	path = disallowedChars.ReplaceAllString(path, "-")
	return multiDash.ReplaceAllString(path, "-")
}

func Dir(ctx context.Context, client *client.Getter, name, baseDir string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	bund, err := bundle.Read(ctx, createName(name, baseDir), baseDir, opts.BundleReader, &bundle.Options{
		Compress:        opts.Compress,
		Labels:          opts.Labels,
		ServiceAccount:  opts.ServiceAccount,
		TargetsFile:     opts.TargetsFile,
		TargetNamespace: opts.TargetNamespace,
		Paused:          opts.Paused,
		SyncGeneration:  opts.SyncGeneration,
		Auth:            opts.Auth,
	})

	if err != nil {
		return err
	}

	def := bund.Definition.DeepCopy()
	def.Namespace = client.Namespace

	if len(def.Spec.Resources) == 0 {
		return ErrNoResources
	}

	objects := []runtime.Object{def}
	for _, scan := range bund.Scans {
		objects = append(objects, scan)
	}

	b, err := yaml.Export(objects...)
	if err != nil {
		return err
	}

	if opts.Output == nil {
		err = save(client, def, bund.Scans...)
	} else {
		_, err = opts.Output.Write(b)
	}

	return err
}

func save(client *client.Getter, bundle *fleet.Bundle, imageScans ...*fleet.ImageScan) error {
	c, err := client.Get()
	if err != nil {
		return err
	}

	obj, err := c.Fleet.Bundle().Get(bundle.Namespace, bundle.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err = c.Fleet.Bundle().Create(bundle); err != nil {
			return err
		}
		logrus.Infof("created: %s/%s\n", bundle.Namespace, bundle.Name)
	} else if err != nil {
		return err
	} else {
		obj.Spec = bundle.Spec
		obj.Annotations = mergeMap(obj.Annotations, bundle.Annotations)
		obj.Labels = mergeMap(obj.Labels, bundle.Labels)
		if _, err := c.Fleet.Bundle().Update(obj); err != nil {
			return err
		}
		logrus.Infof("updated: %s/%s\n", obj.Namespace, obj.Name)
	}

	for _, scan := range imageScans {
		scan.Namespace = client.Namespace
		scan.Spec.GitRepoName = bundle.Labels[fleet.RepoLabel]
		obj, err := c.Fleet.ImageScan().Get(scan.Namespace, scan.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if _, err = c.Fleet.ImageScan().Create(scan); err != nil {
				return err
			}
			logrus.Infof("created (scan): %s/%s\n", bundle.Namespace, bundle.Name)
		} else if err != nil {
			return err
		} else {
			obj.Spec = scan.Spec
			obj.Annotations = mergeMap(obj.Annotations, bundle.Annotations)
			obj.Labels = mergeMap(obj.Labels, bundle.Labels)
			if _, err := c.Fleet.ImageScan().Update(obj); err != nil {
				return err
			}
			logrus.Infof("updated (scan): %s/%s\n", obj.Namespace, obj.Name)
		}
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

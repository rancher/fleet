// Package apply creates bundle resources from gitrepo resources (fleetapply)
package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundlereader"
	"github.com/rancher/fleet/pkg/fleetyaml"
	name2 "github.com/rancher/fleet/pkg/name"

	"github.com/rancher/wrangler/pkg/yaml"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	ErrNoResources = errors.New("no resources found to deploy")
)

type Options struct {
	BundleFile       string
	TargetsFile      string
	Compress         bool
	BundleReader     io.Reader
	Output           io.Writer
	ServiceAccount   string
	TargetNamespace  string
	Paused           bool
	Labels           map[string]string
	SyncGeneration   int64
	Auth             bundlereader.Auth
	HelmRepoURLRegex string
	KeepResources    bool
	AuthByPath       map[string]bundlereader.Auth
}

func globDirs(baseDir string) (result []string, err error) {
	for strings.HasPrefix(baseDir, "/") {
		baseDir = baseDir[1:]
	}
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

// Apply creates bundles from the baseDirs, their names are prefixed with
// repoName. Depending on opts.Output the bundles are created in the cluster or
// printed to stdout, ...
func Apply(ctx context.Context, client *client.Getter, repoName string, baseDirs []string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	if len(baseDirs) == 0 {
		baseDirs = []string{"."}
	}

	foundBundle := false
	gitRepoBundlesMap := make(map[string]bool)
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
				createBundle, e := shouldCreateBundleForThisPath(baseDir, path, info)
				if e != nil {
					return e
				}
				if !createBundle {
					return nil
				}
				if auth, ok := opts.AuthByPath[baseDir]; ok {
					opts.Auth = auth
				}
				if err := Dir(ctx, client, repoName, path, opts, gitRepoBundlesMap); err == ErrNoResources {
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

	if opts.Output == nil {
		err := pruneBundlesNotFoundInRepo(client, repoName, gitRepoBundlesMap)
		if err != nil {
			return err
		}
	}

	if !foundBundle {
		return fmt.Errorf("no resource found at the following paths to deploy: %v", baseDirs)
	}

	return nil
}

// pruneBundlesNotFoundInRepo lists all bundles for this gitrepo and prunes those not found in the repo
func pruneBundlesNotFoundInRepo(client *client.Getter, repoName string, gitRepoBundlesMap map[string]bool) error {
	c, err := client.Get()
	if err != nil {
		return err
	}
	filter := labels.Set(map[string]string{fleet.RepoLabel: repoName})
	bundles, err := c.Fleet.Bundle().List(client.Namespace, metav1.ListOptions{LabelSelector: filter.AsSelector().String()})
	if err != nil {
		return err
	}

	for _, bundle := range bundles.Items {
		if ok := gitRepoBundlesMap[bundle.Name]; !ok {
			logrus.Debugf("Bundle to be deleted since it is not found in gitrepo %v anymore %v %v", repoName, bundle.Namespace, bundle.Name)
			err = c.Fleet.Bundle().Delete(bundle.Namespace, bundle.Name, nil)
			if err != nil {
				return err
			}
		}
	}
	return err
}

// readBundle reads bundle data from a source and returns a bundle with the
// given name, or the name from the raw source file
func readBundle(ctx context.Context, name, baseDir string, opts *Options) (*fleet.Bundle, []*fleet.ImageScan, error) {
	if opts.BundleReader != nil {
		var bundle *fleet.Bundle
		if err := json.NewDecoder(opts.BundleReader).Decode(bundle); err != nil {
			return nil, nil, err
		}
		return bundle, nil, nil
	}

	return bundlereader.Open(ctx, name, baseDir, opts.BundleFile, &bundlereader.Options{
		Compress:         opts.Compress,
		Labels:           opts.Labels,
		ServiceAccount:   opts.ServiceAccount,
		TargetsFile:      opts.TargetsFile,
		TargetNamespace:  opts.TargetNamespace,
		Paused:           opts.Paused,
		SyncGeneration:   opts.SyncGeneration,
		Auth:             opts.Auth,
		HelmRepoURLRegex: opts.HelmRepoURLRegex,
		KeepResources:    opts.KeepResources,
	})
}

// Dir reads a bundle and image scans from a directory and writes runtime objects to the selected output.
//
// name: the gitrepo name, passed to 'fleet apply' on the cli
// basedir: the path from the walk func in Dir, []baseDirs
func Dir(ctx context.Context, client *client.Getter, name, baseDir string, opts *Options, gitRepoBundlesMap map[string]bool) error {
	if opts == nil {
		opts = &Options{}
	}
	// the bundleID is a valid helm release name, it's used as a default if a release name is not specified in helm options
	bundleID := filepath.Join(name, baseDir)
	bundleID = name2.HelmReleaseName(bundleID)

	bundle, scans, err := readBundle(ctx, bundleID, baseDir, opts)
	if err != nil {
		return err
	}

	def := bundle.DeepCopy()
	def.Namespace = client.Namespace

	if len(def.Spec.Resources) == 0 {
		return ErrNoResources
	}
	gitRepoBundlesMap[def.Name] = true

	objects := []runtime.Object{def}
	for _, scan := range scans {
		objects = append(objects, scan)
	}

	b, err := yaml.Export(objects...)
	if err != nil {
		return err
	}

	if opts.Output == nil {
		err = save(client, def, scans...)
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
		logrus.Infof("created: %s/%s", bundle.Namespace, bundle.Name)
	} else if err != nil {
		return err
	} else {
		obj.Spec = bundle.Spec
		obj.Annotations = mergeMap(obj.Annotations, bundle.Annotations)
		obj.Labels = mergeMap(obj.Labels, bundle.Labels)
		if _, err := c.Fleet.Bundle().Update(obj); err != nil {
			return err
		}
		logrus.Infof("updated: %s/%s", obj.Namespace, obj.Name)
	}

	for _, scan := range imageScans {
		scan.Namespace = client.Namespace
		scan.Spec.GitRepoName = bundle.Labels[fleet.RepoLabel]
		obj, err := c.Fleet.ImageScan().Get(scan.Namespace, scan.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if _, err = c.Fleet.ImageScan().Create(scan); err != nil {
				return err
			}
			logrus.Infof("created (scan): %s/%s", bundle.Namespace, bundle.Name)
		} else if err != nil {
			return err
		} else {
			obj.Spec = scan.Spec
			obj.Annotations = mergeMap(obj.Annotations, bundle.Annotations)
			obj.Labels = mergeMap(obj.Labels, bundle.Labels)
			if _, err := c.Fleet.ImageScan().Update(obj); err != nil {
				return err
			}
			logrus.Infof("updated (scan): %s/%s", obj.Namespace, obj.Name)
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

// shouldCreateBundleForThisPath returns true if a bundle should be created for this path. This happens when:
// 1) Root path contains resources in the root directory or any subdirectory without a fleet.yaml.
// 2) Or it is a subdirectory with a fleet.yaml
func shouldCreateBundleForThisPath(baseDir, path string, info os.FileInfo) (bool, error) {
	isRootPath := baseDir == path
	if isRootPath {
		// always create a Bundle if fleet.yaml is found in the root path
		if !fleetyaml.FoundFleetYamlInDirectory(path) {
			// don't create a Bundle if any subdirectory with resources and witouth a fleet.yaml is found
			createBundleForRoot, err := hasSubDirectoryWithResourcesAndWithoutFleetYaml(path)
			if err != nil {
				return false, err
			}
			return createBundleForRoot, nil
		}
	} else {
		if !info.IsDir() {
			return false, nil
		}
		if !fleetyaml.FoundFleetYamlInDirectory(path) {
			return false, nil
		}
	}

	return true, nil
}

// hasSubDirectoryWithResourcesAndWithoutFleetYaml returns true if this path or any of its subdirectories contains any
// resource, and it doesn't contain a fleet.yaml.
func hasSubDirectoryWithResourcesAndWithoutFleetYaml(path string) (bool, error) {
	if fleetyaml.FoundFleetYamlInDirectory(path) {
		return false, nil
	}
	files, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}

	for _, file := range files {
		if !file.IsDir() {
			if ext := filepath.Ext(file.Name()); ext == ".yaml" || ext == ".yml" {
				return true, nil
			}
		} else {
			// check if this subdirectory contains resources without a fleet.yaml. If it contains a fleet.yaml a new
			// Bundle for this subdirectory will be created
			containsResources, err := hasSubDirectoryWithResourcesAndWithoutFleetYaml(filepath.Join(path, file.Name()))
			if err != nil {
				return false, err
			}
			if containsResources {
				return true, nil
			}
		}
	}

	return false, nil
}

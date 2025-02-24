// Package apply creates bundle resources from gitrepo resources.
package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/internal/fleetyaml"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/ociwrapper"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/yaml"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

var (
	ErrNoResources = errors.New("no resources found to deploy")
)

type Getter interface {
	Get() (*client.Client, error)
	GetNamespace() string
}

type OCIRegistrySpec struct {
	Reference       string
	Username        string
	Password        string
	BasicHTTP       bool
	InsecureSkipTLS bool
}

type Options struct {
	BundleFile                  string
	TargetsFile                 string
	Compress                    bool
	BundleReader                io.Reader
	Output                      io.Writer
	ServiceAccount              string
	TargetNamespace             string
	Paused                      bool
	Labels                      map[string]string
	SyncGeneration              int64
	Auth                        bundlereader.Auth
	HelmRepoURLRegex            string
	KeepResources               bool
	DeleteNamespace             bool
	AuthByPath                  map[string]bundlereader.Auth
	CorrectDrift                bool
	CorrectDriftForce           bool
	CorrectDriftKeepFailHistory bool
	OCIRegistry                 OCIRegistrySpec
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

// CreateBundles creates bundles from the baseDirs, their names are prefixed with
// repoName. Depending on opts.Output the bundles are created in the cluster or
// printed to stdout, ...
func CreateBundles(ctx context.Context, client Getter, repoName string, baseDirs []string, opts Options) error {
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
					return fmt.Errorf("writing to bundle output: %w", err)
				}
			}
			err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
				opts := opts
				createBundle, e := shouldCreateBundleForThisPath(baseDir, path, info)
				if e != nil {
					return fmt.Errorf("checking for bundle in path %q: %w", path, err)
				}
				if !createBundle {
					return nil
				}
				if auth, ok := opts.AuthByPath[path]; ok {
					opts.Auth = auth
				}
				if err := Dir(ctx, client, repoName, path, &opts, gitRepoBundlesMap); err == ErrNoResources {
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
func pruneBundlesNotFoundInRepo(client Getter, repoName string, gitRepoBundlesMap map[string]bool) error {
	c, err := client.Get()
	if err != nil {
		return err
	}
	filter := labels.Set(map[string]string{fleet.RepoLabel: repoName})
	bundles, err := c.Fleet.Bundle().List(client.GetNamespace(), metav1.ListOptions{LabelSelector: filter.AsSelector().String()})
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

// newBundle reads bundle data from a source and returns a bundle with the
// given name, or the name from the raw source file
func newBundle(ctx context.Context, name, baseDir string, opts *Options) (*fleet.Bundle, []*fleet.ImageScan, error) {
	if opts.BundleReader != nil {
		var bundle *fleet.Bundle
		if err := json.NewDecoder(opts.BundleReader).Decode(bundle); err != nil {
			return nil, nil, fmt.Errorf("decoding bundle %s: %w", name, err)
		}
		return bundle, nil, nil
	}

	return bundlereader.New(ctx, name, baseDir, opts.BundleFile, &bundlereader.Options{
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
		DeleteNamespace:  opts.DeleteNamespace,
		CorrectDrift: &fleet.CorrectDrift{
			Enabled:         opts.CorrectDrift,
			Force:           opts.CorrectDriftForce,
			KeepFailHistory: opts.CorrectDriftKeepFailHistory,
		},
	})
}

// Dir reads a bundle and image scans from a directory and writes runtime objects to the selected output.
//
// name: the gitrepo name, passed to 'fleet apply' on the cli
// basedir: the path from the walk func in Dir, []baseDirs
func Dir(ctx context.Context, client Getter, name, baseDir string, opts *Options, gitRepoBundlesMap map[string]bool) error {
	if opts == nil {
		opts = &Options{}
	}
	// The bundleID is a valid helm release name, it's used as a default if a release name is not specified in helm options.
	// It's also used to create the bundle name.
	bundleID := filepath.Join(name, baseDir)
	bundleID = names.HelmReleaseName(bundleID)

	bundle, scans, err := newBundle(ctx, bundleID, baseDir, opts)
	if err != nil {
		return err
	}

	bundle = bundle.DeepCopy()
	bundle.Namespace = client.GetNamespace()

	if len(bundle.Spec.Resources) == 0 {
		return ErrNoResources
	}
	gitRepoBundlesMap[bundle.Name] = true

	objects := []runtime.Object{bundle}
	for _, scan := range scans {
		objects = append(objects, scan)
	}

	b, err := yaml.Export(objects...)
	if err != nil {
		return err
	}

	if opts.Output == nil {
		c, err := client.Get()
		if err != nil {
			return err
		}
		if opts.OCIRegistry.Reference == "" {
			if err := save(c, bundle); err != nil {
				return err
			}
		} else {
			if err := saveOCIBundle(ctx, c, bundle, opts); err != nil {
				return err
			}
		}

		if err := saveImageScans(c, bundle, scans); err != nil {
			return err
		}
	} else {
		_, err = opts.Output.Write(b)
	}

	return err
}

func pushOCIManifest(ctx context.Context, bundle *fleet.Bundle, opts *Options) (string, error) {
	manifest := manifest.FromBundle(bundle)
	manifestID, err := manifest.ID()
	if err != nil {
		return "", err
	}
	ociOpts := ociwrapper.OCIOpts{
		Reference:       opts.OCIRegistry.Reference,
		Username:        opts.OCIRegistry.Username,
		Password:        opts.OCIRegistry.Password,
		BasicHTTP:       opts.OCIRegistry.BasicHTTP,
		InsecureSkipTLS: opts.OCIRegistry.InsecureSkipTLS,
	}
	oci := ociwrapper.NewOCIWrapper()
	err = oci.PushManifest(ctx, ociOpts, manifestID, manifest)
	if err != nil {
		return "", err
	}
	return manifestID, nil
}

func save(c *client.Client, bundle *fleet.Bundle) error {
	obj, err := c.Fleet.Bundle().Get(bundle.Namespace, bundle.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if apierrors.IsNotFound(err) {
		if _, err := c.Fleet.Bundle().Create(bundle); err != nil {
			return err
		}
		logrus.Infof("created (bundle): %s/%s", bundle.Namespace, bundle.Name)
	} else {
		obj.Spec = bundle.Spec
		obj.Annotations = bundle.Annotations
		obj.Labels = bundle.Labels

		if _, err := c.Fleet.Bundle().Update(obj); err != nil {
			return err
		}
		logrus.Infof("updated (bundle): %s/%s", obj.Namespace, obj.Name)
	}

	return nil
}

func saveImageScans(c *client.Client, bundle *fleet.Bundle, scans []*fleet.ImageScan) error {
	for _, scan := range scans {
		scan.Namespace = bundle.Namespace
		scan.Spec.GitRepoName = bundle.Labels[fleet.RepoLabel]
		obj, err := c.Fleet.ImageScan().Get(scan.Namespace, scan.Name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}

		if apierrors.IsNotFound(err) {
			if _, err = c.Fleet.ImageScan().Create(scan); err != nil {
				return err
			}
			logrus.Infof("created (scan): %s/%s", bundle.Namespace, bundle.Name)
		} else {
			obj.Spec = scan.Spec
			obj.Annotations = bundle.Annotations
			obj.Labels = bundle.Labels
			if _, err := c.Fleet.ImageScan().Update(obj); err != nil {
				return err
			}
			logrus.Infof("updated (scan): %s/%s", obj.Namespace, obj.Name)
		}
	}
	return nil
}

func saveOCIBundle(ctx context.Context, c *client.Client, bundle *fleet.Bundle, opts *Options) error {
	manifestID, err := pushOCIManifest(ctx, bundle, opts)
	if err != nil {
		return err
	}
	logrus.Infof("OCI artifact stored successful: %s %s", bundle.Name, manifestID)

	obj, err := c.Fleet.Bundle().Get(bundle.Namespace, bundle.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if apierrors.IsNotFound(err) {
		// We don't store the resources in the bundle. Just keep the manifestID for
		// being able to access the bundle's contents later.
		bundle.Spec.Resources = nil
		bundle.Spec.ContentsID = manifestID
		obj, err = c.Fleet.Bundle().Create(bundle)
		if err != nil {
			return err
		}

		if err := createOrUpdate(c, newOCISecret(manifestID, obj, opts)); err != nil {
			return err
		}
		logrus.Infof("createOrUpdate (oci secret): %s/%s", obj.Namespace, obj.Name)
	} else {
		obj.Spec = bundle.Spec
		obj.Annotations = bundle.Annotations
		obj.Labels = bundle.Labels
		obj.Spec.Resources = nil
		obj.Spec.ContentsID = manifestID
		obj, err = c.Fleet.Bundle().Update(obj)
		if err != nil {
			return err
		}

		if err := createOrUpdate(c, newOCISecret(manifestID, obj, opts)); err != nil {
			return err
		}
		logrus.Infof("createOrUpdate (oci secret): %s/%s", obj.Namespace, obj.Name)
	}

	return nil
}

// when using the OCI registry manifestID won't be empty
// In this case we need to create a secret to store the
// OCI registry reference and credentials so the fleet controller is
// able to access.
func newOCISecret(manifestID string, bundle *fleet.Bundle, opts *Options) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      manifestID,
			Namespace: bundle.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         fleet.SchemeGroupVersion.String(),
					Kind:               "Bundle",
					Name:               bundle.GetName(),
					UID:                bundle.GetUID(),
					BlockOwnerDeletion: ptr.To(true),
					Controller:         ptr.To(true),
				},
			},
		},
		Data: map[string][]byte{
			ociwrapper.OCISecretReference: []byte(opts.OCIRegistry.Reference),
			ociwrapper.OCISecretUsername:  []byte(opts.OCIRegistry.Username),
			ociwrapper.OCISecretPassword:  []byte(opts.OCIRegistry.Password),
			ociwrapper.OCISecretBasicHTTP: []byte(strconv.FormatBool(opts.OCIRegistry.BasicHTTP)),
			ociwrapper.OCISecretInsecure:  []byte(strconv.FormatBool(opts.OCIRegistry.InsecureSkipTLS)),
		},
	}
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

func createOrUpdate(c *client.Client, obj *corev1.Secret) error {
	_, err := c.Core.Secret().Get(obj.GetNamespace(), obj.GetName(), metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get secret %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}

	if apierrors.IsNotFound(err) {
		_, err := c.Core.Secret().Create(obj)
		return err
	}

	_, err = c.Core.Secret().Update(obj)
	return err
}

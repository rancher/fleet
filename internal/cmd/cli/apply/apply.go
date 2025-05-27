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
	"github.com/rancher/fleet/internal/fleetyaml"
	"github.com/rancher/fleet/internal/helmvalues"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
	Namespace                   string
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
	OCIRegistrySecret           string
	DrivenScan                  bool
	DrivenScanSeparator         string
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
func CreateBundles(ctx context.Context, client client.Client, repoName string, baseDirs []string, opts Options) error {
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
				// needed as opts are mutated in this loop
				opts := opts

				if err != nil {
					return fmt.Errorf("failed walking path %q: %w", path, err)
				}
				if info.IsDir() && info.Name() == ".git" {
					return filepath.SkipDir
				}

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
		err := pruneBundlesNotFoundInRepo(ctx, client, repoName, opts.Namespace, gitRepoBundlesMap)
		if err != nil {
			return err
		}
	}

	if !foundBundle {
		return fmt.Errorf("no resource found at the following paths to deploy: %v", baseDirs)
	}

	return nil
}

// CreateBundlesDriven creates bundles from the given baseDirs. Those bundles' names will be prefixed with
// repoName. Depending on opts.Output the bundles are created in the cluster or
// printed to stdout, ...
// CreateBundlesDriven does not scan the given dirs recursively, it simply considers each of them
// to be the base path for a bundle.
// The given baseDirs may describe a simple path or a path and a fleet file,
// separated by a character set in opts.
// If no fleet file is provided it tries to load a fleet.yaml in the root of the dir, or will consider
// the directory as a raw content folder.
func CreateBundlesDriven(ctx context.Context, client client.Client, repoName string, baseDirs []string, opts Options) error {
	if len(baseDirs) == 0 {
		baseDirs = []string{"."}
	}

	foundBundle := false
	gitRepoBundlesMap := make(map[string]bool)
	for _, baseDir := range baseDirs {
		opts := opts
		// verify if it also defines a fleetFile
		var err error
		baseDir, opts.BundleFile, err = getPathAndFleetYaml(baseDir, opts.DrivenScanSeparator)
		if err != nil {
			return err
		}
		if auth, ok := opts.AuthByPath[baseDir]; ok {
			opts.Auth = auth
		}
		if err := Dir(ctx, client, repoName, baseDir, &opts, gitRepoBundlesMap); err == ErrNoResources {
			logrus.Warnf("%s: %v", baseDir, err)
			return nil
		} else if err != nil {
			return err
		}
		foundBundle = true
	}

	if opts.Output == nil {
		err := pruneBundlesNotFoundInRepo(ctx, client, repoName, opts.Namespace, gitRepoBundlesMap)
		if err != nil {
			return err
		}
	}

	if !foundBundle {
		return fmt.Errorf("no resource found at the following paths to deploy: %v", baseDirs)
	}

	return nil
}

// getPathAndFleetYaml returns the path and options file from a given path.
// The path and options file should be separated by the given separator
func getPathAndFleetYaml(path, separator string) (string, string, error) {
	baseDirFleetFile := strings.Split(path, separator)
	if len(baseDirFleetFile) == 2 {
		return baseDirFleetFile[0], baseDirFleetFile[1], nil
	}

	if len(baseDirFleetFile) > 2 {
		return "", "", fmt.Errorf("invalid bundle path: %q", path)
	}

	return path, "", nil
}

// pruneBundlesNotFoundInRepo lists all bundles for this gitrepo and prunes those not found in the repo
func pruneBundlesNotFoundInRepo(ctx context.Context, c client.Client, repoName, ns string, gitRepoBundlesMap map[string]bool) error {
	filter := labels.SelectorFromSet(labels.Set{fleet.RepoLabel: repoName})
	bundleList := &fleet.BundleList{}
	err := c.List(ctx, bundleList, &client.ListOptions{LabelSelector: filter, Namespace: ns})

	for _, bundle := range bundleList.Items {
		if ok := gitRepoBundlesMap[bundle.Name]; !ok {
			logrus.Debugf("Bundle to be deleted since it is not found in gitrepo %v anymore %v %v", repoName, bundle.Namespace, bundle.Name)
			err = c.Delete(ctx, &bundle)
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

	return bundlereader.NewBundle(ctx, name, baseDir, opts.BundleFile, &bundlereader.Options{
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
func Dir(ctx context.Context, client client.Client, name, baseDir string, opts *Options, gitRepoBundlesMap map[string]bool) error {
	if opts == nil {
		opts = &Options{}
	}
	// The bundleID is a valid helm release name, it's used as a default if a release name is not specified in helm options.
	// It's also used to create the bundle name.
	bundleID := filepath.Join(name, baseDir)
	if opts.BundleFile != "" {
		bundleID = filepath.Join(bundleID, strings.TrimSuffix(opts.BundleFile, filepath.Ext(opts.BundleFile)))
	}
	bundleID = names.HelmReleaseName(bundleID)

	bundle, scans, err := newBundle(ctx, bundleID, baseDir, opts)
	if err != nil {
		return err
	}

	bundle.Namespace = opts.Namespace

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
		h, data, err := helmvalues.ExtractValues(bundle)
		if err != nil {
			return err
		}

		// If values were found in the bundle the hash is not empty, we
		// remove the values from the bundle. Also, delete any old
		// secret if the values are empty.
		if h != "" {
			helmvalues.ClearValues(bundle)
		} else if err := deleteSecretIfExists(ctx, client, bundle.Name, bundle.Namespace); err != nil {
			return err
		}
		bundle.Spec.ValuesHash = h

		var ociOpts ocistorage.OCIOpts
		secretOCIRegistryID := types.NamespacedName{Name: opts.OCIRegistrySecret, Namespace: bundle.Namespace}
		useOCIRegistry, err := shouldStoreInOCIRegistry(ctx, client, secretOCIRegistryID, &ociOpts)
		if err != nil {
			return err
		}
		if useOCIRegistry {
			if bundle, err = saveOCIBundle(ctx, client, bundle, ociOpts); err != nil {
				return err
			}
		} else {
			if bundle, err = save(ctx, client, bundle); err != nil {
				return err
			}
		}

		// Saves the Helm values as a secret. The secret is owned by
		// the bundle. It will not create a secret if the values are
		// empty.
		if len(data) > 0 {
			valuesSecret := newValuesSecret(bundle, data)
			updated := valuesSecret.DeepCopy()
			_, err = controllerutil.CreateOrUpdate(ctx, client, valuesSecret, func() error {
				valuesSecret.Labels = updated.Labels
				valuesSecret.Data = updated.Data
				valuesSecret.Type = updated.Type
				return nil
			})
			if err != nil {
				return err
			}
		}

		if err := saveImageScans(ctx, client, bundle, scans); err != nil {
			return err
		}
	} else {
		_, err = opts.Output.Write(b)
	}

	return err
}

func shouldStoreInOCIRegistry(ctx context.Context, c client.Reader, ociSecretKey types.NamespacedName, ociOpts *ocistorage.OCIOpts) (bool, error) {
	if !ocistorage.ExperimentalOCIIsEnabled() {
		return false, nil
	}

	opts, err := ocistorage.ReadOptsFromSecret(ctx, c, ociSecretKey)
	if err != nil {
		if apierrors.IsNotFound(err) && ociSecretKey.Name == "" {
			// don't return not found errors when no secret name was specified by the user
			return false, nil
		}
		return false, err
	}
	ociOpts.Reference = opts.Reference
	ociOpts.Username = opts.Username
	ociOpts.Password = opts.Password
	ociOpts.AgentUsername = opts.AgentUsername
	ociOpts.AgentPassword = opts.AgentPassword
	ociOpts.BasicHTTP = opts.BasicHTTP
	ociOpts.InsecureSkipTLS = opts.InsecureSkipTLS

	return true, nil
}

func pushOCIManifest(ctx context.Context, bundle *fleet.Bundle, opts ocistorage.OCIOpts) (string, error) {
	manifest := manifest.FromBundle(bundle)
	manifestID, err := manifest.ID()
	if err != nil {
		return "", err
	}
	oci := ocistorage.NewOCIWrapper()
	err = oci.PushManifest(ctx, opts, manifestID, manifest)
	if err != nil {
		return "", err
	}
	return manifestID, nil
}

func save(ctx context.Context, c client.Client, bundle *fleet.Bundle) (*fleet.Bundle, error) {
	updated := bundle.DeepCopy()
	result, err := controllerutil.CreateOrUpdate(ctx, c, bundle, func() error {
		if bundle != nil && bundle.Spec.HelmOpOptions != nil {
			return fmt.Errorf("a helmOps bundle with name %q already exists", bundle.Name)
		}

		bundle.Spec = updated.Spec
		bundle.Annotations = updated.Annotations
		bundle.Labels = updated.Labels
		return nil
	})
	if err != nil {
		return nil, err
	}
	logrus.Infof("%s (bundle): %s/%s", result, bundle.Namespace, bundle.Name)

	return bundle, nil
}

func saveImageScans(ctx context.Context, c client.Client, bundle *fleet.Bundle, scans []*fleet.ImageScan) error {
	for _, scan := range scans {
		scan.Namespace = bundle.Namespace
		scan.Spec.GitRepoName = bundle.Labels[fleet.RepoLabel]
		updated := scan.DeepCopy()
		result, err := controllerutil.CreateOrUpdate(ctx, c, scan, func() error {
			scan.Spec = updated.Spec
			scan.Annotations = bundle.Annotations
			scan.Labels = bundle.Labels
			return nil
		})
		if err != nil {
			return err
		}
		logrus.Infof("%s (scan): %s/%s", result, scan.Namespace, scan.Name)
	}
	return nil
}

func saveOCIBundle(ctx context.Context, c client.Client, bundle *fleet.Bundle, opts ocistorage.OCIOpts) (*fleet.Bundle, error) {
	manifestID, err := pushOCIManifest(ctx, bundle, opts)
	if err != nil {
		return bundle, err
	}
	logrus.Infof("OCI artifact stored successfully: %s %s", bundle.Name, manifestID)

	updated := bundle.DeepCopy()
	_, err = controllerutil.CreateOrUpdate(ctx, c, bundle, func() error {
		if bundle != nil && bundle.Spec.HelmOpOptions != nil {
			return fmt.Errorf("a helmOps bundle with name %q already exists", bundle.Name)
		}

		bundle.Spec = updated.Spec
		bundle.Annotations = updated.Annotations
		bundle.Labels = updated.Labels

		// We don't store the resources in the bundle. Just keep the manifestID for
		// being able to access the bundle's contents later.
		bundle.Spec.Resources = nil
		bundle.Spec.ContentsID = manifestID
		return nil
	})
	if err != nil {
		return nil, err
	}

	secret := newOCISecret(manifestID, bundle, opts)
	data := secret.Data
	result, err := controllerutil.CreateOrUpdate(ctx, c, secret, func() error {
		secret.Data = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	logrus.Infof("%s (oci secret): %s/%s", result, bundle.Namespace, bundle.Name)

	return bundle, nil
}

// when using the OCI registry manifestID won't be empty
// In this case we need to create a secret to store the
// OCI registry reference and credentials so the fleet controller is
// able to access.
func newOCISecret(manifestID string, bundle *fleet.Bundle, opts ocistorage.OCIOpts) *corev1.Secret {
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
			ocistorage.OCISecretReference:     []byte(opts.Reference),
			ocistorage.OCISecretUsername:      []byte(opts.Username),
			ocistorage.OCISecretPassword:      []byte(opts.Password),
			ocistorage.OCISecretAgentUsername: []byte(opts.AgentUsername),
			ocistorage.OCISecretAgentPassword: []byte(opts.AgentPassword),
			ocistorage.OCISecretBasicHTTP:     []byte(strconv.FormatBool(opts.BasicHTTP)),
			ocistorage.OCISecretInsecure:      []byte(strconv.FormatBool(opts.InsecureSkipTLS)),
		},
		Type: fleet.SecretTypeOCIStorage,
	}
}

func newValuesSecret(bundle *fleet.Bundle, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		Type: fleet.SecretTypeBundleValues,
		ObjectMeta: metav1.ObjectMeta{
			Name:      bundle.Name,
			Namespace: bundle.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         fleet.SchemeGroupVersion.String(),
					Kind:               "Bundle",
					Name:               bundle.Name,
					UID:                bundle.GetUID(),
					BlockOwnerDeletion: ptr.To(true),
					Controller:         ptr.To(true),
				},
			},
			Labels: bundle.Labels,
		},

		Data: data,
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

func deleteSecretIfExists(ctx context.Context, c client.Client, name, ns string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	if err := c.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	return nil
}

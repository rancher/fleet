// Package apply creates bundle resources from gitrepo resources.
package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/content"
	"github.com/rancher/fleet/internal/fleetyaml"
	"github.com/rancher/fleet/internal/helmvalues"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetevent "github.com/rancher/fleet/pkg/event"

	"github.com/rancher/wrangler/v3/pkg/yaml"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	k8syaml "sigs.k8s.io/yaml"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	ErrNoResources = errors.New("no resources found to deploy")
)

const (
	JSONOutputEnvVar             = "FLEET_JSON_OUTPUT"
	JobNameEnvVar                = "JOB_NAME"
	FleetApplyConflictRetriesEnv = "FLEET_APPLY_CONFLICT_RETRIES"
	defaultApplyConflictRetries  = 1
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
	JobNameEnvVar               string
}

type bundleWithOpts struct {
	bundle *fleet.Bundle
	scans  []*fleet.ImageScan
	opts   *Options
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

const bundleCreationMaxConcurrency = 4

// CreateBundles creates bundles from the baseDirs, their names are prefixed with
// repoName. Depending on opts.Output the bundles are created in the cluster or
// printed to stdout, ...
func CreateBundles(pctx context.Context, client client.Client, r record.EventRecorder, repoName string, baseDirs []string, opts Options) error {
	if len(baseDirs) == 0 {
		baseDirs = []string{"."}
	}

	// Using an errgroup to manage concurrency
	// 1. Goroutines will be launched, honouring the concurrency limit, and eventually block trying to write to `bundlesChan`.
	// 2. The main function will read from `bundlesChan`, hence unblocking the goroutines. This will continue to read from `bundlesChan` until it is closed.
	// 3. We use another goroutine to wait for all goroutines to finish, then close `bundlesChan`, finally unblocking the main function.

	bundlesChan := make(chan *bundleWithOpts)
	eg, ctx := errgroup.WithContext(pctx)
	eg.SetLimit(bundleCreationMaxConcurrency + 1) // extra goroutine for WalkDir loop
	eg.Go(func() error {
		for _, baseDir := range baseDirs {
			matches, err := globDirs(baseDir)
			if err != nil {
				return fmt.Errorf("invalid path glob %s: %w", baseDir, err)
			}
			for _, baseDir := range matches {
				if err := filepath.WalkDir(baseDir, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return fmt.Errorf("failed walking path %q: %w", path, err)
					}
					if entry.IsDir() && entry.Name() == ".git" {
						return filepath.SkipDir
					}
					createBundle, e := shouldCreateBundleForThisPath(baseDir, path, entry)
					if e != nil {
						return fmt.Errorf("checking for bundle in path %q: %w", path, err)
					}
					if !createBundle {
						return nil
					}

					// needed as opts are mutated in this loop
					opts := opts
					eg.Go(func() error {
						if err := setAuthByPath(&opts, path); err != nil {
							return err
						}

						bundle, scans, err := bundleFromDir(ctx, repoName, path, opts)
						if err != nil {
							if err == ErrNoResources {
								logrus.Warnf("%s: %v", path, err)
								return nil
							}
							return err
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						case bundlesChan <- &bundleWithOpts{bundle: bundle, scans: scans, opts: &opts}:
						}
						return nil
					})
					return nil
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	go func() {
		_ = eg.Wait()
		close(bundlesChan)
	}()

	gitRepoBundlesMap := make(map[string]*fleet.Bundle)
	var bundlesToWrite []*bundleWithOpts
	for b := range bundlesChan {
		gitRepoBundlesMap[b.bundle.Name] = b.bundle
		bundlesToWrite = append(bundlesToWrite, b)
	}
	// Recovers any error that could happen in the errgroup, won't actually wait
	if err := eg.Wait(); err != nil {
		return err
	}
	ctx = pctx // context from ErrorGroup is canceled after the first Wait() returns

	if opts.Output == nil {
		err := pruneBundlesNotFoundInRepo(ctx, client, repoName, opts.Namespace, gitRepoBundlesMap)
		if err != nil {
			return err
		}
	}

	if len(gitRepoBundlesMap) == 0 {
		return fmt.Errorf("no resource found at the following paths to deploy: %v", baseDirs)
	}

	egWrite, ctx := errgroup.WithContext(pctx)
	egWrite.SetLimit(bundleCreationMaxConcurrency)
	for _, b := range bundlesToWrite {
		egWrite.Go(func() error {
			return writeBundle(ctx, client, r, b.bundle, b.scans, *b.opts)
		})
	}

	return egWrite.Wait()
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
func CreateBundlesDriven(pctx context.Context, client client.Client, r record.EventRecorder, repoName string, baseDirs []string, opts Options) error {
	if len(baseDirs) == 0 {
		baseDirs = []string{"."}
	}

	// Using an errgroup to manage concurrency
	// 1. Goroutines will be launched, honouring the concurrency limit, and eventually block trying to write to `bundlesChan`.
	// 2. The main function will read from `bundlesChan`, hence unblocking the goroutines. This will continue to read from `bundlesChan` until it is closed.
	// 3. We use another goroutine to wait for all goroutines to finish, then close `bundlesChan`, finally unblocking the main function.
	bundlesChan := make(chan *bundleWithOpts)
	eg, ctx := errgroup.WithContext(pctx)
	eg.SetLimit(bundleCreationMaxConcurrency + 1) // extra goroutine for WalkDir loop
	eg.Go(func() error {
		for _, baseDir := range baseDirs {
			opts := opts
			eg.Go(func() error {
				// verify if it also defines a fleetFile
				var err error
				baseDir, opts.BundleFile, err = getPathAndFleetYaml(baseDir, opts.DrivenScanSeparator)
				if err != nil {
					return err
				}

				if err := setAuthByPath(&opts, baseDir); err != nil {
					return err
				}

				bundle, scans, err := bundleFromDir(ctx, repoName, baseDir, opts)
				if err != nil {
					if err == ErrNoResources {
						logrus.Warnf("%s: %v", baseDir, err)
						return nil
					}
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case bundlesChan <- &bundleWithOpts{bundle: bundle, scans: scans, opts: &opts}:
				}
				return nil
			})
		}
		return nil
	})
	go func() {
		_ = eg.Wait()
		close(bundlesChan)
	}()

	gitRepoBundlesMap := make(map[string]*fleet.Bundle)
	var bundlesToWrite []*bundleWithOpts
	for b := range bundlesChan {
		gitRepoBundlesMap[b.bundle.Name] = b.bundle
		bundlesToWrite = append(bundlesToWrite, b)
	}
	// Recovers any error that could happen in the errgroup, won't actually wait
	if err := eg.Wait(); err != nil {
		return err
	}
	ctx = pctx // context from ErrorGroup is canceled after the first Wait() returns

	if opts.Output == nil {
		err := pruneBundlesNotFoundInRepo(ctx, client, repoName, opts.Namespace, gitRepoBundlesMap)
		if err != nil {
			return err
		}
	}

	if len(gitRepoBundlesMap) == 0 {
		return fmt.Errorf("no resource found at the following paths to deploy: %v", baseDirs)
	}

	egWrite, ctx := errgroup.WithContext(pctx)
	egWrite.SetLimit(bundleCreationMaxConcurrency)
	for _, b := range bundlesToWrite {
		egWrite.Go(func() error {
			return writeBundle(ctx, client, r, b.bundle, b.scans, *b.opts)
		})
	}

	return egWrite.Wait()
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
func pruneBundlesNotFoundInRepo(
	ctx context.Context,
	c client.Client,
	repoName,
	ns string,
	gitRepoBundlesMap map[string]*fleet.Bundle,
) error {
	filter := labels.SelectorFromSet(labels.Set{fleet.RepoLabel: repoName})
	bundleList := &fleet.BundleList{}
	err := c.List(ctx, bundleList, &client.ListOptions{LabelSelector: filter, Namespace: ns})

	for _, bundle := range bundleList.Items {
		if _, ok := gitRepoBundlesMap[bundle.Name]; !ok {
			logrus.Debugf("Bundle to be deleted since it is not found in gitrepo %v anymore %v %v", repoName, bundle.Namespace, bundle.Name)

			for _, inClusterRsc := range bundle.Spec.Resources {
				for _, grb := range gitRepoBundlesMap {
					logrus.Debugf("gitRepo bundle: %v", grb)
					for _, grRsc := range grb.Spec.Resources { // FIXME nil pointer here: are resources not populated?
						if inClusterRsc.Name != grRsc.Name {
							continue
						}

						logrus.Debugf("resources: [in cluster] %v\n, [in gitrepo] %v", inClusterRsc, grRsc)

						ow1, err := getKindNS(grRsc, grb.Name)
						if err != nil {
							// XXX: error
							continue
						}
						if ow1.Kind == "" {
							// Skipping non-manifest resources, e.g. Chart.yaml and values
							// files.
							continue
						}

						ow2, err := getKindNS(inClusterRsc, bundle.Name)
						if err != nil {
							// XXX: error
							continue
						}
						if ow2.Kind == "" {
							continue
						}

						if ow1.Kind == ow2.Kind && ow1.Name == ow2.Name && ow1.Namespace == ow2.Namespace {
							// Warning: this will not work with bundlenamespacemappings

							grb.Spec.Overwrites = append(grb.Spec.Overwrites, ow1)
						}
					}
				}
			}
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
func newBundle(ctx context.Context, name, baseDir string, opts Options) (*fleet.Bundle, []*fleet.ImageScan, error) {
	var bundle *fleet.Bundle
	var scans []*fleet.ImageScan
	if opts.BundleReader != nil {
		if err := json.NewDecoder(opts.BundleReader).Decode(bundle); err != nil {
			return nil, nil, fmt.Errorf("decoding bundle %s: %w", name, err)
		}
	} else {
		var err error
		bundle, scans, err = bundlereader.NewBundle(ctx, name, baseDir, opts.BundleFile, &bundlereader.Options{
			BundleFile:       opts.BundleFile,
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
		if err != nil {
			return nil, nil, err
		}
	}
	bundle.Namespace = opts.Namespace
	return bundle, scans, nil
}

// bundleFromDir reads a specific directory and produces a bundle and image scans.
//
// name: the gitrepo name, passed to 'fleet apply' on the cli
// basedir: a directory containing a Bundle, as observed by CreateBundles or CreateBundlesDriven
func bundleFromDir(ctx context.Context, name, baseDir string, opts Options) (*fleet.Bundle, []*fleet.ImageScan, error) {
	// The bundleID is a valid helm release name, it's used as a default if a release name is not specified in helm options.
	// It's also used to create the bundle name.
	bundleID := filepath.Join(name, baseDir)
	if opts.BundleFile != "" {
		bundleID = filepath.Join(bundleID, strings.TrimSuffix(opts.BundleFile, filepath.Ext(opts.BundleFile)))
	}
	bundleID = names.HelmReleaseName(bundleID)

	bundle, scans, err := newBundle(ctx, bundleID, baseDir, opts)
	if err != nil {
		return nil, nil, err
	} else if len(bundle.Spec.Resources) == 0 {
		return nil, nil, ErrNoResources
	}
	return bundle, scans, nil
}

func writeBundle(ctx context.Context, c client.Client, r record.EventRecorder, bundle *fleet.Bundle, scans []*fleet.ImageScan, opts Options) error {
	// Early return for "offline" mode, only printing the result to stdout/file
	if opts.Output != nil {
		return printToOutput(opts.Output, bundle, scans)
	}

	// We need to exit early if the bundle is being deleted
	tmp := &fleet.Bundle{}
	if err := c.Get(ctx, client.ObjectKey{Name: bundle.Name, Namespace: bundle.Namespace}, tmp); err == nil {
		if tmp.DeletionTimestamp != nil {
			return fmt.Errorf("the bundle %q is being deleted, cannot create during a delete operation", bundle.Name)
		}
	}

	h, data, err := helmvalues.ExtractValues(bundle)
	if err != nil {
		return err
	}

	// If values were found in the bundle the hash is not empty, we
	// remove the values from the bundle. Also, delete any old
	// secret if the values are empty.
	if h != "" {
		helmvalues.ClearValues(bundle)
	} else if err := deleteSecretIfExists(ctx, c, bundle.Name, bundle.Namespace); err != nil {
		return err
	}
	bundle.Spec.ValuesHash = h

	var ociOpts ocistorage.OCIOpts
	secretOCIRegistryID := client.ObjectKey{Name: opts.OCIRegistrySecret, Namespace: bundle.Namespace}
	useOCIRegistry, err := shouldStoreInOCIRegistry(ctx, c, secretOCIRegistryID, &ociOpts)
	if err != nil {
		return err
	}
	if useOCIRegistry {
		if bundle, err = saveOCIBundle(ctx, c, r, bundle, ociOpts); err != nil {
			return err
		}
	} else {
		if bundle, err = save(ctx, c, bundle); err != nil {
			return err
		}
	}

	// Saves the Helm values as a secret. The secret is owned by
	// the bundle. It will not create a secret if the values are
	// empty.
	if len(data) > 0 {
		valuesSecret := newValuesSecret(bundle, data)
		updated := valuesSecret.DeepCopy()
		_, err = controllerutil.CreateOrUpdate(ctx, c, valuesSecret, func() error {
			valuesSecret.OwnerReferences = updated.OwnerReferences
			valuesSecret.Labels = updated.Labels
			valuesSecret.Data = updated.Data
			valuesSecret.Type = updated.Type
			return nil
		})
		if err != nil {
			return err
		}
	}

	return saveImageScans(ctx, c, bundle, scans)
}

func printToOutput(w io.Writer, bundle *fleet.Bundle, scans []*fleet.ImageScan) error {
	objects := []runtime.Object{bundle}
	for _, scan := range scans {
		objects = append(objects, scan)
	}

	b, err := yaml.Export(objects...)
	if err != nil {
		return err
	}

	_, err = w.Write(b)
	return err
}

func shouldStoreInOCIRegistry(ctx context.Context, c client.Reader, ociSecretKey client.ObjectKey, ociOpts *ocistorage.OCIOpts) (bool, error) {
	if !ocistorage.OCIIsEnabled() {
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
		if bundle.Spec.HelmOpOptions != nil {
			return fmt.Errorf("a helmOps bundle with name %q already exists", bundle.Name)
		}
		// We cannot update a bundle that is going to be deleted, our update would be lost
		if bundle.DeletionTimestamp != nil {
			return fmt.Errorf("the bundle %q is being deleted", bundle.Name)
		}

		if bundle.Spec.ContentsID != "" {
			// this bundle was previously deployed to an OCI registry.
			// Delete the OCI artifact as it's no longer required.
			if err := deleteOCIManifest(ctx, c, bundle, ocistorage.OCIOpts{}); err != nil {
				// we log the error and continue, since the OCI registry is an external entity to the the cluster
				// we may encounter various types of transient errors (such as connection or access issues).
				logrus.Warnf("deleting OCI artifact: %v", err)
				return err

			}
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

func saveOCIBundle(ctx context.Context, c client.Client, r record.EventRecorder, bundle *fleet.Bundle, opts ocistorage.OCIOpts) (*fleet.Bundle, error) {
	manifestID, err := pushOCIManifest(ctx, bundle, opts)
	if err != nil {
		return bundle, err
	}
	logrus.Infof("OCI artifact stored successfully: %s %s", bundle.Name, manifestID)

	updated := bundle.DeepCopy()
	_, err = controllerutil.CreateOrUpdate(ctx, c, bundle, func() error {
		if bundle.DeletionTimestamp != nil {
			return fmt.Errorf("the bundle %q is being deleted", bundle.Name)
		}

		if bundle.Spec.HelmOpOptions != nil {
			return fmt.Errorf("a helmOps bundle with name %q already exists", bundle.Name)
		}

		// If the current manifestID is different from the previous one,
		// delete the previous OCI artifact
		if bundle.Spec.ContentsID != "" && bundle.Spec.ContentsID != manifestID {
			if err := deleteOCIManifest(ctx, c, bundle, opts); err != nil {
				// we log the error and continue, since the OCI registry is an external entity to the the cluster
				// we may encounter various types of transient errors (such as connection or access issues).
				logrus.Warnf("deleting OCI artifact: %v", err)
				sendWarningEvent(r, bundle.Namespace, bundle.Spec.ContentsID, err)
			}
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

func deleteOCIManifest(ctx context.Context, c client.Client, bundle *fleet.Bundle, opts ocistorage.OCIOpts) error {
	if bundle.Spec.ContentsID == "" {
		return nil
	}
	secretID := client.ObjectKey{Name: bundle.Spec.ContentsID, Namespace: bundle.Namespace}
	if opts.Reference == "" {
		// we don't have the reference details, get them from the bundle's secret
		var err error
		opts, err = ocistorage.ReadOptsFromSecret(ctx, c, secretID)
		if err != nil {
			return err
		}
	}
	if err := ocistorage.NewOCIWrapper().DeleteManifest(ctx, opts, bundle.Spec.ContentsID); err != nil {
		return err
	}

	// also delete the bundle secret as it's no longer needed
	secretToDelete := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bundle.Spec.ContentsID,
			Namespace: bundle.Namespace,
		},
	}
	if err := c.Delete(ctx, secretToDelete); err != nil {
		return err
	}

	return nil
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
			Labels:    map[string]string{fleet.InternalSecretLabel: "true"},
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
func shouldCreateBundleForThisPath(baseDir, path string, entry fs.DirEntry) (bool, error) {
	isRootPath := baseDir == path
	if isRootPath {
		// always create a Bundle if fleet.yaml is found in the root path
		if !fleetyaml.FoundFleetYamlInDirectory(path) {
			// don't create a Bundle if any subdirectory with resources and without a fleet.yaml is found
			createBundleForRoot, err := hasSubDirectoryWithResourcesAndWithoutFleetYaml(path)
			if err != nil {
				return false, err
			}
			return createBundleForRoot, nil
		}
	} else {
		if !entry.IsDir() {
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

func sendWarningEvent(r record.EventRecorder, namespace, artifactID string, errorToLog error) {
	jobName := os.Getenv(JobNameEnvVar)
	if jobName == "" {
		logrus.Warnf("%q environment variable not set", JobNameEnvVar)
		return
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
		},
	}
	r.Event(job, fleetevent.Warning, "FailedToDeleteOCIArtifact", fmt.Sprintf("deleting OCI artifact %q: %v", artifactID, errorToLog.Error()))
}

func setAuthByPath(opts *Options, path string) error {
	if auth, ok := opts.AuthByPath[path]; ok {
		opts.Auth = auth

		return nil
	}

	// No direct match; check for globs instead.
	var patternKeys []string
	for k := range opts.AuthByPath {
		patternKeys = append(patternKeys, k)
	}
	// Sort patterns in lexical order to work around
	// non-deterministic iteration order for Go maps.
	slices.Sort(patternKeys)

	for _, pattern := range patternKeys {
		isMatch, err := filepath.Match(pattern, path)
		if err != nil {
			return fmt.Errorf("failed to check for matches in auth paths: %w", err)
		}

		if isMatch {
			opts.Auth = opts.AuthByPath[pattern]
			break
		}
	}

	return nil
}

func GetOnConflictRetries() (int, error) {
	s := os.Getenv(FleetApplyConflictRetriesEnv)
	if s != "" {
		// check if we have a valid value
		// it must be an integer
		r, err := strconv.Atoi(s)
		if err != nil {
			return defaultApplyConflictRetries, err
		} else {
			return r, nil
		}
	}

	return defaultApplyConflictRetries, nil
}

type k8sWithNS struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

func getKindNS(br fleet.BundleResource, bundleName string) (fleet.OverwrittenResource, error) {
	var contents []byte
	var err error
	if br.Encoding == "base64+gz" {
		contents, err = content.GUnzip([]byte(br.Content))
		if err != nil {
			logrus.Debugf("could not uncompress contents of resource %s in bundle %s;"+
				" skipping overlap detection for this resource", br.Name, bundleName)
			return fleet.OverwrittenResource{}, nil
		}
	} else {
		// encoding should be empty
		contents = []byte(br.Content)
	}

	// Replace templating tags to prevent unmarshalling errors. We are not interested in the resource contents
	// beyond its kind, name and namespace.
	placeholder := "TEMPLATED"
	templating := regexp.MustCompile("{{[^}]+}}")
	c := templating.ReplaceAll(contents, []byte(placeholder))

	var rsc k8sWithNS
	err = k8syaml.Unmarshal(c, &rsc)
	if err != nil {
		return fleet.OverwrittenResource{}, fmt.Errorf("could not convert resource contents into object: %w", err)
	}

	logrus.Debugf("contents from bundle resource: %v", string(contents))

	or := fleet.OverwrittenResource{
		Kind:      rsc.Kind,
		Name:      rsc.Name,
		Namespace: rsc.Namespace,
	}
	logrus.Debugf("returning overwritten resource: %v", or)
	return or, nil
}

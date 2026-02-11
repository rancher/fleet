package helmdeployer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/kube"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"

	"github.com/rancher/fleet/internal/experimental"
	"github.com/rancher/fleet/internal/helmdeployer/render"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type dryRunConfig struct {
	DryRun       bool
	DryRunOption string
}

// Deploy deploys an unpacked content resource with helm. bundleID is the name of the bundledeployment.
func (h *Helm) Deploy(ctx context.Context, bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions) (*releasev1.Release, error) {
	if options.Helm == nil {
		options.Helm = &fleet.HelmOptions{}
	}
	if options.Kustomize == nil {
		options.Kustomize = &fleet.KustomizeOptions{}
	}

	tar, err := render.HelmChart(bundleID, manifest, options)
	if err != nil {
		return nil, err
	}

	chart, err := loader.LoadArchive(tar)
	if err != nil {
		return nil, err
	}

	if chart.Metadata.Annotations == nil {
		chart.Metadata.Annotations = map[string]string{}
	}
	chart.Metadata.Annotations[ServiceAccountNameAnnotation] = options.ServiceAccount
	chart.Metadata.Annotations[BundleIDAnnotation] = bundleID
	chart.Metadata.Annotations[AgentNamespaceAnnotation] = h.agentNamespace
	chart.Metadata.Annotations[KeepResourcesAnnotation] = strconv.FormatBool(options.KeepResources)

	if manifest.Commit != "" {
		chart.Metadata.Annotations[CommitAnnotation] = manifest.Commit
	}

	if release, err := h.install(ctx, bundleID, manifest, chart, options, getDryRunConfig(chart, true)); err != nil {
		return nil, err
	} else if h.template {
		return release, nil
	}

	return h.install(ctx, bundleID, manifest, chart, options, getDryRunConfig(chart, false))
}

// install runs helm install or upgrade and supports dry running the action. Will run helm rollback in case of a failed upgrade.
func (h *Helm) install(ctx context.Context, bundleID string, manifest *manifest.Manifest, chart *chartv2.Chart, options fleet.BundleDeploymentOptions, dryRunCfg dryRunConfig) (*releasev1.Release, error) {
	logger := log.FromContext(ctx).WithName("helm-deployer").WithName("install").WithValues("commit", manifest.Commit, "dryRun", dryRunCfg.DryRun)
	timeout, defaultNamespace, releaseName := h.getOpts(bundleID, options)

	values, err := h.getValues(ctx, options, defaultNamespace)
	if err != nil {
		return nil, err
	}

	cfg, err := h.getCfg(ctx, defaultNamespace, options.ServiceAccount)
	if err != nil {
		return nil, err
	}

	uninstall, err := h.mustUninstall(cfg, releaseName)
	if err != nil {
		return nil, err
	}

	if uninstall {
		logger.Info("Uninstalling helm release first")
		if err := h.delete(ctx, bundleID, options, dryRunCfg.DryRun); err != nil {
			return nil, err
		}
		if dryRunCfg.DryRun {
			// In dry run mode, we've validated that uninstall is needed but can't proceed
			// with install/upgrade since the old release conceptually still exists.
			// Returning (nil, nil) indicates successful dry run completion with no release object.
			return nil, nil
		}
	}

	install, err := h.mustInstall(cfg, releaseName)
	if err != nil {
		return nil, err
	}

	pr, err := h.createPostRenderer(cfg, bundleID, manifest, chart, options)
	if err != nil {
		return nil, err
	}

	if install {
		return h.runInstall(ctx, cfg, chart, values, releaseName, defaultNamespace, timeout, options, pr, dryRunCfg)
	}

	return h.runUpgrade(ctx, cfg, chart, values, releaseName, defaultNamespace, timeout, options, pr, dryRunCfg)
}

// createPostRenderer creates a post-renderer for Helm charts that handles label/annotation
// transformations and CRD deletion policies based on Fleet bundle deployment options.
func (h *Helm) createPostRenderer(cfg *action.Configuration, bundleID string, manifest *manifest.Manifest, chart *chartv2.Chart, options fleet.BundleDeploymentOptions) (*postRender, error) {
	pr := &postRender{
		labelPrefix: h.labelPrefix,
		labelSuffix: h.labelSuffix,
		bundleID:    bundleID,
		manifest:    manifest,
		opts:        options,
		chart:       chart,
	}

	if !h.useGlobalCfg {
		mapper, err := cfg.RESTClientGetter.ToRESTMapper()
		if err != nil {
			return nil, err
		}
		pr.mapper = mapper
	}

	return pr, nil
}

// runInstall executes a Helm install operation with the provided configuration and values.
// It creates an Install action, configures it, and runs the installation.
func (h *Helm) runInstall(
	ctx context.Context,
	cfg *action.Configuration,
	chart *chartv2.Chart,
	values map[string]interface{},
	releaseName string,
	namespace string,
	timeout time.Duration,
	options fleet.BundleDeploymentOptions,
	pr *postRender,
	dryRunCfg dryRunConfig,
) (*releasev1.Release, error) {
	logger := log.FromContext(ctx)
	u := action.NewInstall(cfg)

	h.configureInstallAction(u, cfg, releaseName, namespace, timeout, options, pr, dryRunCfg)

	if !dryRunCfg.DryRun {
		logger.Info("Installing helm release")
	}

	rel, err := u.Run(chart, values)
	if err != nil {
		return nil, err
	}

	return assertRelease(rel)
}

// configureDryRunStrategy sets the DryRunStrategy based on template mode and dryRunConfig.
// Template mode requires DryRunClient to render without cluster interaction.
// If DryRunOption is "server", use DryRunServer to allow lookup functions to query the cluster.
// Otherwise, use DryRunClient for client-only dry run or DryRunNone for actual execution.
func (h *Helm) configureDryRunStrategy(dryRunCfg dryRunConfig) action.DryRunStrategy {
	if h.template {
		return action.DryRunClient
	} else if dryRunCfg.DryRun {
		if dryRunCfg.DryRunOption == "server" {
			return action.DryRunServer
		}
		return action.DryRunClient
	}
	return action.DryRunNone
}

// configureInstallAction configures a Helm Install action with Fleet-specific options,
// including timeout, wait strategies, and dry-run configuration.
func (h *Helm) configureInstallAction(u *action.Install, cfg *action.Configuration, releaseName, namespace string, timeout time.Duration, options fleet.BundleDeploymentOptions, pr *postRender, dryRunCfg dryRunConfig) {
	if cfg.Capabilities != nil {
		if cfg.Capabilities.KubeVersion.Version != "" {
			u.KubeVersion = &cfg.Capabilities.KubeVersion
		}
		if cfg.Capabilities.APIVersions != nil {
			u.APIVersions = cfg.Capabilities.APIVersions
		}
	}
	u.TakeOwnership = options.Helm.TakeOwnership
	u.ForceReplace = options.Helm.Force || (options.CorrectDrift != nil && options.CorrectDrift.Force)
	// Disable server-side apply when taking ownership or forcing replacement to avoid conflicts.
	// When adopting existing resources or forcing replacement, we need three-way merge instead.
	if u.TakeOwnership || u.ForceReplace {
		u.ServerSideApply = false
	} else {
		// Explicitly enable server-side apply when neither TakeOwnership nor ForceReplace is set
		u.ServerSideApply = true
	}
	u.EnableDNS = !options.Helm.DisableDNS
	u.Replace = true
	u.RollbackOnFailure = options.Helm.Atomic
	u.ReleaseName = releaseName
	u.CreateNamespace = true
	u.Namespace = namespace
	u.Timeout = timeout
	u.DryRunStrategy = h.configureDryRunStrategy(dryRunCfg)
	u.SkipSchemaValidation = options.Helm.SkipSchemaValidation
	u.PostRenderer = pr
	u.WaitForJobs = options.Helm.WaitForJobs
	// When timeout is set, use StatusWatcherStrategy to wait for resources.
	// Otherwise use HookOnlyStrategy (the default, equivalent to not waiting).
	if u.Timeout > 0 {
		u.WaitStrategy = kube.StatusWatcherStrategy
	} else {
		u.WaitStrategy = kube.HookOnlyStrategy
	}
}

// runUpgrade executes a Helm upgrade operation with the provided configuration and values.
// It creates an Upgrade action, configures it, and runs the upgrade with automatic rollback
// retry logic if the upgrade is interrupted.
func (h *Helm) runUpgrade(
	ctx context.Context,
	cfg *action.Configuration,
	chart *chartv2.Chart,
	values map[string]interface{},
	releaseName string,
	namespace string,
	timeout time.Duration,
	options fleet.BundleDeploymentOptions,
	pr *postRender,
	dryRunCfg dryRunConfig,
) (*releasev1.Release, error) {
	logger := log.FromContext(ctx)
	u := action.NewUpgrade(cfg)

	h.configureUpgradeAction(u, namespace, timeout, options, pr, dryRunCfg)

	if !dryRunCfg.DryRun {
		logger.Info("Upgrading helm release")
	}

	rel, err := u.Run(releaseName, chart, values)
	if err != nil && err.Error() == HelmUpgradeInterruptedError {
		return h.retryUpgradeAfterRollback(ctx, cfg, u, releaseName, chart, values)
	}
	if err != nil {
		return nil, err
	}

	return assertRelease(rel)
}

// configureUpgradeAction configures a Helm Upgrade action with Fleet-specific options,
// including timeout, wait strategies, and drift correction settings.
func (h *Helm) configureUpgradeAction(u *action.Upgrade, namespace string, timeout time.Duration, options fleet.BundleDeploymentOptions, pr *postRender, dryRunCfg dryRunConfig) {
	u.TakeOwnership = true
	u.EnableDNS = !options.Helm.DisableDNS
	u.ForceReplace = options.Helm.Force
	if options.CorrectDrift != nil {
		u.ForceReplace = u.ForceReplace || options.CorrectDrift.Force
	}
	// When using ForceReplace, must disable ServerSideApply.
	// ForceReplace and ServerSideApply cannot be used together in Helm v4.
	// Set to "false" (not "auto") to explicitly disable server-side apply.
	// Otherwise use "auto" to respect the previous release's apply method.
	if u.ForceReplace {
		u.ServerSideApply = "false"
	} else {
		u.ServerSideApply = "auto"
	}
	u.RollbackOnFailure = options.Helm.Atomic
	u.MaxHistory = options.Helm.MaxHistory
	if u.MaxHistory == 0 {
		u.MaxHistory = MaxHelmHistory
	}
	u.Namespace = namespace
	u.Timeout = timeout
	u.DryRunStrategy = h.configureDryRunStrategy(dryRunCfg)
	u.SkipSchemaValidation = options.Helm.SkipSchemaValidation
	u.DisableOpenAPIValidation = h.template || dryRunCfg.DryRun
	u.PostRenderer = pr
	u.WaitForJobs = options.Helm.WaitForJobs
	// When timeout is set, use StatusWatcherStrategy to wait for resources.
	// Otherwise use HookOnlyStrategy (the default, equivalent to not waiting).
	if u.Timeout > 0 {
		u.WaitStrategy = kube.StatusWatcherStrategy
	} else {
		u.WaitStrategy = kube.HookOnlyStrategy
	}
}

// retryUpgradeAfterRollback handles the case where a Helm upgrade is interrupted and retries
// the upgrade after performing a rollback. This addresses the "another operation is in progress" error.
func (h *Helm) retryUpgradeAfterRollback(ctx context.Context, cfg *action.Configuration, u *action.Upgrade, releaseName string, chart *chartv2.Chart, values map[string]interface{}) (*releasev1.Release, error) {
	logger := log.FromContext(ctx)
	logger.Info("Helm doing a rollback", "error", HelmUpgradeInterruptedError)

	r := action.NewRollback(cfg)
	err := r.Run(releaseName)
	if err != nil {
		return nil, err
	}

	logger.V(1).Info("Retrying upgrade after rollback")
	rel, err := u.Run(releaseName, chart, values)
	if err != nil {
		return nil, err
	}

	return assertRelease(rel)
}

// assertRelease converts a Helm release interface to a concrete *releasev1.Release type.
func assertRelease(rel interface{}) (*releasev1.Release, error) {
	if v1Rel, ok := rel.(*releasev1.Release); ok {
		return v1Rel, nil
	}
	return nil, fmt.Errorf("unexpected release type: %T", rel)
}

func (h *Helm) mustUninstall(cfg *action.Configuration, releaseName string) (bool, error) {
	r, err := getLastRelease(cfg.Releases, releaseName)
	if err != nil {
		// If the release doesn't exist, there's nothing to uninstall
		if errors.Is(err, driver.ErrReleaseNotFound) || errors.Is(err, driver.ErrNoDeployedReleases) {
			return false, nil
		}
		return false, err
	}
	return r.Info.Status == releasecommon.StatusUninstalling || r.Info.Status == releasecommon.StatusPendingInstall, nil
}

// mustInstall checks if a fresh install is required by verifying if there is no deployed release.
// Returns true if no deployed release exists for the given release name.
func (h *Helm) mustInstall(cfg *action.Configuration, releaseName string) (bool, error) {
	_, err := cfg.Releases.Deployed(releaseName)
	if err != nil && errors.Is(err, driver.ErrNoDeployedReleases) {
		return true, nil
	}
	return false, err
}

func (h *Helm) getValues(ctx context.Context, options fleet.BundleDeploymentOptions, defaultNamespace string) (map[string]interface{}, error) {
	if options.Helm == nil {
		return nil, nil
	}

	var values map[string]interface{}
	if options.Helm.Values != nil {
		values = options.Helm.Values.Data
	}

	// avoid the possibility of returning a nil map
	if values == nil {
		values = map[string]interface{}{}
	}
	// do not run this when using template
	if !h.template {
		for _, valuesFrom := range options.Helm.ValuesFrom {
			var tempValues map[string]interface{}
			if valuesFrom.ConfigMapKeyRef != nil {
				name := valuesFrom.ConfigMapKeyRef.Name
				namespace := valuesFrom.ConfigMapKeyRef.Namespace
				if namespace == "" || isInDownstreamResources(name, "ConfigMap", options) {
					// If the namespace is not set, or if the ConfigMap is part of the copied resources,
					// we assume it is in the default namespace of the Helm release.
					namespace = defaultNamespace
				}
				key := valuesFrom.ConfigMapKeyRef.Key
				if key == "" {
					key = DefaultKey
				}
				configMap := &corev1.ConfigMap{}
				err := h.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, configMap)
				if err != nil {
					return nil, err
				}
				tempValues, err = valuesFromConfigMap(name, namespace, key, configMap)
				if err != nil {
					return nil, err
				}
			}
			if tempValues != nil {
				values = mergeValues(values, tempValues)
				tempValues = nil
			}

			// merge secret last to be compatible with fleet <= 0.6.0
			if valuesFrom.SecretKeyRef != nil {
				name := valuesFrom.SecretKeyRef.Name
				namespace := valuesFrom.SecretKeyRef.Namespace
				if namespace == "" || isInDownstreamResources(name, "Secret", options) {
					// If the namespace is not set, or if the Secret is part of the copied resources,
					// we assume it is in the default namespace of the Helm release.
					namespace = defaultNamespace
				}
				key := valuesFrom.SecretKeyRef.Key
				if key == "" {
					key = DefaultKey
				}
				secret := &corev1.Secret{}
				err := h.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, secret)
				if err != nil {
					return nil, err
				}
				tempValues, err = valuesFromSecret(name, namespace, key, secret)
				if err != nil {
					return nil, err
				}
			}
			if tempValues != nil {
				values = mergeValues(values, tempValues)
			}
		}
	}

	return values, nil
}

func valuesFromSecret(name, namespace, key string, secret *corev1.Secret) (map[string]interface{}, error) {
	var m map[string]interface{}
	if secret == nil {
		return m, nil
	}

	values, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("key %s is missing from secret %s/%s, can't use it in valuesFrom", key, namespace, name)
	}
	if err := yaml.NewYAMLToJSONDecoder(bytes.NewBuffer(values)).Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func valuesFromConfigMap(name, namespace, key string, configMap *corev1.ConfigMap) (map[string]interface{}, error) {
	var m map[string]interface{}
	if configMap == nil {
		return m, nil
	}

	values, ok := configMap.Data[key]
	if !ok {
		return nil, fmt.Errorf("key %s is missing from configmap %s/%s, can't use it in valuesFrom", key, namespace, name)
	}
	if err := yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(values)).Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func mergeMaps(base, other map[string]string) map[string]string {
	result := map[string]string{}
	for k, v := range base {
		result[k] = v
	}
	for k, v := range other {
		result[k] = v
	}
	return result
}

// mergeValues merges source and destination map, preferring values over maps
// from the source values. This is slightly adapted from:
// https://github.com/helm/helm/blob/2332b480c9cb70a0d8a85247992d6155fbe82416/cmd/helm/install.go#L359
func mergeValues(dest, src map[string]interface{}) map[string]interface{} {
	for k, v := range src {
		// If the key doesn't exist already, then just set the key to that value
		if _, exists := dest[k]; !exists {
			// new key
			dest[k] = v
			continue
		}
		nextMap, ok := v.(map[string]interface{})
		// If it isn't another map, overwrite the value
		if !ok {
			// new key is not a map, overwrite existing key as we prefer values over maps
			dest[k] = v
			continue
		}
		// Edge case: If the key exists in the destination, but isn't a map
		destMap, isMap := dest[k].(map[string]interface{})
		// If the source map has a map for this key, prefer it
		if !isMap {
			dest[k] = v
			continue
		}
		// If we got to this point, it is a map in both, so merge them
		dest[k] = mergeValues(destMap, nextMap)
	}
	return dest
}

// getDryRunConfig determines the dry-run configuration based on whether the chart
// uses the Helm "lookup" function.
// If the chart contains the "lookup" function, DryRunOption is set to "server"
// to allow the lookup function to interact with the Kubernetes API during a dry-run.
// Otherwise, DryRunOption remains empty, implying a client-side dry-run.
func getDryRunConfig(chart *chartv2.Chart, dryRun bool) dryRunConfig {
	cfg := dryRunConfig{DryRun: dryRun}
	if dryRun && hasLookupFunction(chart) {
		cfg.DryRunOption = "server"
	}

	return cfg
}

// isInDownstreamResources returns true when a resource with the
// provided name exists in the provided BundleDeploymentOptions.DownstreamResources slice.
// If not found, returns false.
func isInDownstreamResources(resourceName, kind string, options fleet.BundleDeploymentOptions) bool {
	kind = strings.ToLower(kind)
	if !experimental.CopyResourcesDownstreamEnabled() {
		return false
	}

	for _, dr := range options.DownstreamResources {
		if dr.Name == resourceName && strings.ToLower(dr.Kind) == kind {
			return true
		}
	}
	return false
}

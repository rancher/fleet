package helmdeployer

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/helmdeployer/helmcache"
	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"

	"github.com/rancher/fleet/internal/helmdeployer/kustomize"
	"github.com/rancher/fleet/internal/helmdeployer/rawyaml"
	"github.com/rancher/fleet/internal/helmdeployer/render"
	"github.com/rancher/fleet/internal/manifest"
	name2 "github.com/rancher/fleet/internal/name"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v2/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/v2/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v2/pkg/kv"
	"github.com/rancher/wrangler/v2/pkg/name"
	"github.com/rancher/wrangler/v2/pkg/yaml"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const (
	BundleIDAnnotation           = "fleet.cattle.io/bundle-id"
	CommitAnnotation             = "fleet.cattle.io/commit"
	AgentNamespaceAnnotation     = "fleet.cattle.io/agent-namespace"
	ServiceAccountNameAnnotation = "fleet.cattle.io/service-account"
	DefaultServiceAccount        = "fleet-default"
	KeepResourcesAnnotation      = "fleet.cattle.io/keep-resources"
	HelmUpgradeInterruptedError  = "another operation (install/upgrade/rollback) is in progress"
	MaxHelmHistory               = 2
	CRDKind                      = "CustomResourceDefinition"
)

var (
	ErrNoRelease    = errors.New("failed to find release")
	ErrNoResourceID = errors.New("no resource ID available")
	DefaultKey      = "values.yaml"
)

type postRender struct {
	labelPrefix string
	labelSuffix string
	bundleID    string
	manifest    *manifest.Manifest
	chart       *chart.Chart
	mapper      meta.RESTMapper
	opts        fleet.BundleDeploymentOptions
}

type Helm struct {
	agentNamespace      string
	serviceAccountCache corecontrollers.ServiceAccountCache
	configmapCache      corecontrollers.ConfigMapCache
	secretCache         corecontrollers.SecretCache
	getter              genericclioptions.RESTClientGetter
	globalCfg           action.Configuration
	// useGlobalCfg is only used by Template
	useGlobalCfg     bool
	template         bool
	defaultNamespace string
	labelPrefix      string
	labelSuffix      string
}

type Resources struct {
	ID               string           `json:"id,omitempty"`
	DefaultNamespace string           `json:"defaultNamespace,omitempty"`
	Objects          []runtime.Object `json:"objects,omitempty"`
}

type DeployedBundle struct {
	// BundleID is the bundle.Name
	BundleID string
	// ReleaseName is actually in the form "namespace/release name"
	ReleaseName string
	// KeepResources indicate if resources should be kept when deleting a GitRepo or Bundle
	KeepResources bool
}

func NewHelm(namespace, defaultNamespace, labelPrefix, labelSuffix string, getter genericclioptions.RESTClientGetter,
	serviceAccountCache corecontrollers.ServiceAccountCache, configmapCache corecontrollers.ConfigMapCache, secretCache corecontrollers.SecretCache) (*Helm, error) {
	h := &Helm{
		getter:              getter,
		defaultNamespace:    defaultNamespace,
		agentNamespace:      namespace,
		serviceAccountCache: serviceAccountCache,
		configmapCache:      configmapCache,
		secretCache:         secretCache,
		labelPrefix:         labelPrefix,
		labelSuffix:         labelSuffix,
	}
	cfg, err := h.createCfg("")
	if err != nil {
		return nil, err
	}
	h.globalCfg = cfg

	return h, nil
}

func (p *postRender) Run(renderedManifests *bytes.Buffer) (modifiedManifests *bytes.Buffer, err error) {
	data := renderedManifests.Bytes()

	objs, err := yaml.ToObjects(bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	if len(objs) == 0 {
		data = nil
	}

	// Kustomize applies some restrictions fleet does not have, like a regular expression, which checks for valid file
	// names. If no instructions for kustomize are found in the manifests, then kustomize shouldn't be called at all
	// to prevent causing issues with these restrictions.
	kustomizable := false
	for _, resource := range p.manifest.Resources {
		if strings.HasSuffix(resource.Name, "kustomization.yaml") ||
			strings.HasSuffix(resource.Name, "kustomization.yml") ||
			strings.HasSuffix(resource.Name, "Kustomization") {
			kustomizable = true
			break
		}
	}
	if kustomizable {
		newObjs, processed, err := kustomize.Process(p.manifest, data, p.opts.Kustomize.Dir)
		if err != nil {
			return nil, err
		}
		if processed {
			objs = newObjs
		}
	}

	yamlObjs, err := rawyaml.ToObjects(p.chart)
	if err != nil {
		return nil, err
	}
	objs = append(objs, yamlObjs...)

	setID := GetSetID(p.bundleID, p.labelPrefix, p.labelSuffix)
	labels, annotations, err := apply.GetLabelsAndAnnotations(setID, nil)
	if err != nil {
		return nil, err
	}

	for _, obj := range objs {
		m, err := meta.Accessor(obj)
		if err != nil {
			return nil, err
		}
		if !p.opts.DeleteCRDResources &&
			obj.GetObjectKind().GroupVersionKind().Kind == CRDKind {
			annotations[kube.ResourcePolicyAnno] = kube.KeepPolicy
		}
		m.SetLabels(mergeMaps(m.GetLabels(), labels))
		m.SetAnnotations(mergeMaps(m.GetAnnotations(), annotations))

		if p.opts.TargetNamespace != "" {
			if p.mapper != nil {
				gvk := obj.GetObjectKind().GroupVersionKind()
				mapping, err := p.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
				if err != nil {
					return nil, err
				}
				if mapping.Scope.Name() == meta.RESTScopeNameRoot {
					apiVersion, kind := gvk.ToAPIVersionAndKind()
					return nil, fmt.Errorf("invalid cluster scoped object [name=%s kind=%v apiVersion=%s] found, consider using \"defaultNamespace\", not \"namespace\" in fleet.yaml", m.GetName(),
						kind, apiVersion)
				}
			}
			m.SetNamespace(p.opts.TargetNamespace)
		}
	}
	data, err = yaml.ToBytes(objs)
	return bytes.NewBuffer(data), err
}

func (h *Helm) Deploy(bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions) (*Resources, error) {
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

	if options.Helm.SkipSchemaValidation {
		// TODO: instead of manipulating the chart object, use helm's own functionality when it's available:
		//       https://github.com/helm/helm/pull/11510
		chart.Schema = nil
	}

	if resources, err := h.install(bundleID, manifest, chart, options, true); err != nil {
		return nil, err
	} else if h.template {
		return releaseToResources(resources)
	}

	release, err := h.install(bundleID, manifest, chart, options, false)
	if err != nil {
		return nil, err
	}

	return releaseToResources(release)
}

// RemoveExternalChanges does a helm rollback to remove changes made outside of fleet.
// It removes the helm history entry if the rollback fails.
func (h *Helm) RemoveExternalChanges(bd *fleet.BundleDeployment) (*Resources, error) {
	logrus.Infof("Drift correction: rollback BundleDeployment %s", bd.Name)

	_, defaultNamespace, releaseName := h.getOpts(bd.Name, bd.Spec.Options)
	cfg, err := h.getCfg(defaultNamespace, bd.Spec.Options.ServiceAccount)
	if err != nil {
		return nil, err
	}
	currentRelease, err := cfg.Releases.Last(releaseName)
	if err != nil {
		return nil, err
	}

	r := action.NewRollback(&cfg)
	r.Version = currentRelease.Version
	r.MaxHistory = bd.Spec.Options.Helm.MaxHistory
	if r.MaxHistory == 0 {
		r.MaxHistory = MaxHelmHistory
	}
	if bd.Spec.CorrectDrift.Force {
		r.Force = true
	}
	if err = r.Run(releaseName); err != nil {
		if bd.Spec.CorrectDrift.KeepFailHistory {
			return nil, err
		}
		return nil, removeFailedRollback(cfg, currentRelease, err)
	}

	release, err := cfg.Releases.Last(releaseName)
	if err != nil {
		return nil, err
	}
	return releaseToResources(release)
}

func (h *Helm) mustUninstall(cfg *action.Configuration, releaseName string) (bool, error) {
	r, err := cfg.Releases.Last(releaseName)
	if err != nil {
		return false, nil
	}
	return r.Info.Status == release.StatusUninstalling, err
}

func (h *Helm) mustInstall(cfg *action.Configuration, releaseName string) (bool, error) {
	_, err := cfg.Releases.Deployed(releaseName)
	if err != nil && strings.Contains(err.Error(), "has no deployed releases") {
		return true, nil
	}
	return false, err
}

func (h *Helm) getOpts(bundleID string, options fleet.BundleDeploymentOptions) (time.Duration, string, string) {
	if options.Helm == nil {
		options.Helm = &fleet.HelmOptions{}
	}

	var timeout time.Duration
	if options.Helm.TimeoutSeconds > 0 {
		timeout = time.Second * time.Duration(options.Helm.TimeoutSeconds)
	}

	ns := options.DefaultNamespace
	if options.TargetNamespace != "" {
		ns = options.TargetNamespace
	}

	if ns == "" {
		ns = h.defaultNamespace
	}

	if options.Helm != nil && options.Helm.ReleaseName != "" {
		// JSON schema validation makes sure that the option is valid
		return timeout, ns, options.Helm.ReleaseName
	}

	// releaseName has a limit of 53 in helm https://github.com/helm/helm/blob/main/pkg/action/install.go#L58
	// fleet apply already produces valid names, but we need to make sure
	// that bundles from other sources are valid
	return timeout, ns, name2.HelmReleaseName(bundleID)
}

func (h *Helm) getCfg(namespace, serviceAccountName string) (action.Configuration, error) {
	var (
		cfg    action.Configuration
		getter = h.getter
	)

	if h.useGlobalCfg {
		return h.globalCfg, nil
	}

	serviceAccountNamespace, serviceAccountName, err := h.getServiceAccount(serviceAccountName)
	if err != nil {
		return cfg, err
	}

	if serviceAccountName != "" {
		getter, err = newImpersonatingGetter(serviceAccountNamespace, serviceAccountName, h.getter)
		if err != nil {
			return cfg, err
		}
	}

	kClient := kube.New(getter)
	kClient.Namespace = namespace

	cfg, err = h.createCfg(namespace)
	cfg.Releases.MaxHistory = MaxHelmHistory
	cfg.KubeClient = kClient

	cfg.Capabilities, _ = getCapabilities(cfg)

	return cfg, err
}

func (h *Helm) install(bundleID string, manifest *manifest.Manifest, chart *chart.Chart, options fleet.BundleDeploymentOptions, dryRun bool) (*release.Release, error) {
	timeout, defaultNamespace, releaseName := h.getOpts(bundleID, options)

	values, err := h.getValues(options, defaultNamespace)
	if err != nil {
		return nil, err
	}

	cfg, err := h.getCfg(defaultNamespace, options.ServiceAccount)
	if err != nil {
		return nil, err
	}

	uninstall, err := h.mustUninstall(&cfg, releaseName)
	if err != nil {
		return nil, err
	}

	if uninstall {
		if err := h.delete(bundleID, options, dryRun); err != nil {
			return nil, err
		}
		if dryRun {
			return nil, nil
		}
	}

	install, err := h.mustInstall(&cfg, releaseName)
	if err != nil {
		return nil, err
	}

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

	if install {
		u := action.NewInstall(&cfg)
		u.ClientOnly = h.template || dryRun
		if cfg.Capabilities != nil {
			if cfg.Capabilities.KubeVersion.Version != "" {
				u.KubeVersion = &cfg.Capabilities.KubeVersion
			}
			if cfg.Capabilities.APIVersions != nil {
				u.APIVersions = cfg.Capabilities.APIVersions
			}
		}
		u.ForceAdopt = options.Helm.TakeOwnership
		u.EnableDNS = !options.Helm.DisableDNS
		u.Replace = true
		u.ReleaseName = releaseName
		u.CreateNamespace = true
		u.Namespace = defaultNamespace
		u.Timeout = timeout
		u.DryRun = dryRun
		u.PostRenderer = pr
		u.WaitForJobs = options.Helm.WaitForJobs
		if u.Timeout > 0 {
			u.Wait = true
		}
		if !dryRun {
			logrus.Infof("Helm: Installing %s", bundleID)
		}
		return u.Run(chart, values)
	}

	u := action.NewUpgrade(&cfg)
	u.Adopt = true
	u.EnableDNS = !options.Helm.DisableDNS
	u.Force = options.Helm.Force
	u.Atomic = options.Helm.Atomic
	u.MaxHistory = options.Helm.MaxHistory
	if u.MaxHistory == 0 {
		u.MaxHistory = MaxHelmHistory
	}
	u.Namespace = defaultNamespace
	u.Timeout = timeout
	u.DryRun = dryRun
	u.DisableOpenAPIValidation = h.template || dryRun
	u.PostRenderer = pr
	u.WaitForJobs = options.Helm.WaitForJobs
	if u.Timeout > 0 {
		u.Wait = true
	}
	if !dryRun {
		logrus.Infof("Helm: Upgrading %s", bundleID)
	}
	rel, err := u.Run(releaseName, chart, values)
	if err != nil && err.Error() == HelmUpgradeInterruptedError {
		logrus.Infof("Helm error: %s for %s. Doing a rollback", HelmUpgradeInterruptedError, bundleID)
		r := action.NewRollback(&cfg)
		err = r.Run(releaseName)
		if err != nil {
			return nil, err
		}
		logrus.Debugf("Helm: retrying upgrade for %s after rollback", bundleID)

		return u.Run(releaseName, chart, values)
	}

	return rel, err
}

func (h *Helm) getValues(options fleet.BundleDeploymentOptions, defaultNamespace string) (map[string]interface{}, error) {
	if options.Helm == nil {
		return nil, nil
	}

	var values map[string]interface{}
	if options.Helm.Values != nil {
		values = options.Helm.Values.Data
	}

	// do not run this when using template
	if !h.template {
		for _, valuesFrom := range options.Helm.ValuesFrom {
			var tempValues map[string]interface{}
			if valuesFrom.ConfigMapKeyRef != nil {
				name := valuesFrom.ConfigMapKeyRef.Name
				namespace := valuesFrom.ConfigMapKeyRef.Namespace
				if namespace == "" {
					namespace = defaultNamespace
				}
				key := valuesFrom.ConfigMapKeyRef.Key
				if key == "" {
					key = DefaultKey
				}
				configMap, err := h.configmapCache.Get(namespace, name)
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
				if namespace == "" {
					namespace = defaultNamespace
				}
				key := valuesFrom.SecretKeyRef.Key
				if key == "" {
					key = DefaultKey
				}
				secret, err := h.secretCache.Get(namespace, name)
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

// ListDeployments returns a list of bundles by listing all helm releases via
// helm's storage driver (secrets)
func (h *Helm) ListDeployments() ([]DeployedBundle, error) {
	list := action.NewList(&h.globalCfg)
	list.All = true
	releases, err := list.Run()
	if err != nil {
		return nil, err
	}

	var (
		result []DeployedBundle
	)

	for _, release := range releases {
		d := release.Chart.Metadata.Annotations[BundleIDAnnotation]
		if d == "" {
			continue
		}
		ns := release.Chart.Metadata.Annotations[AgentNamespaceAnnotation]
		if ns != "" && ns != h.agentNamespace {
			continue
		}
		// ignore error as keepResources should be false if annotation not found
		keepResources, _ := strconv.ParseBool(release.Chart.Metadata.Annotations[KeepResourcesAnnotation])
		result = append(result, DeployedBundle{
			BundleID:      d,
			ReleaseName:   release.Namespace + "/" + release.Name,
			KeepResources: keepResources,
		})
	}

	return result, nil
}

func getReleaseNameVersionAndNamespace(bundleID, resourcesID string) (string, int, string, error) {
	// When a bundle is installed a resourcesID is generated. If there is no
	// resourcesID then there isn't anything to lookup.
	if resourcesID == "" {
		return "", 0, "", ErrNoResourceID
	}
	namespace, name := kv.Split(resourcesID, "/")
	releaseName, versionStr := kv.Split(name, ":")
	version, _ := strconv.Atoi(versionStr)

	if releaseName == "" {
		releaseName = bundleID
	}

	return releaseName, version, namespace, nil
}

func (h *Helm) getRelease(releaseName, namespace string, version int) (*release.Release, error) {
	hist := action.NewHistory(&h.globalCfg)

	releases, err := hist.Run(releaseName)
	if err == driver.ErrReleaseNotFound {
		return nil, ErrNoRelease
	} else if err != nil {
		return nil, err
	}

	for _, release := range releases {
		if release.Name == releaseName && release.Version == version && release.Namespace == namespace {
			return release, nil
		}
	}

	return nil, ErrNoRelease
}

func (h *Helm) EnsureInstalled(bundleID, resourcesID string) (bool, error) {
	releaseName, version, namespace, err := getReleaseNameVersionAndNamespace(bundleID, resourcesID)
	if err != nil {
		return false, err
	}

	if _, err := h.getRelease(releaseName, namespace, version); err == ErrNoRelease {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// Resources returns the resources from the helm release history
func (h *Helm) Resources(bundleID, resourcesID string) (*Resources, error) {
	releaseName, version, namespace, err := getReleaseNameVersionAndNamespace(bundleID, resourcesID)
	if err != nil {
		return &Resources{}, err
	}

	release, err := h.getRelease(releaseName, namespace, version)
	if err == ErrNoRelease {
		return &Resources{}, nil
	} else if err != nil {
		return nil, err
	}
	return releaseToResources(release)
}

func (h *Helm) ResourcesFromPreviousReleaseVersion(bundleID, resourcesID string) (*Resources, error) {
	releaseName, version, namespace, err := getReleaseNameVersionAndNamespace(bundleID, resourcesID)
	if err != nil {
		return &Resources{}, err
	}

	release, err := h.getRelease(releaseName, namespace, version-1)
	if err == ErrNoRelease {
		return &Resources{}, nil
	} else if err != nil {
		return nil, err
	}
	return releaseToResources(release)
}

// Delete the release for the given bundleID. releaseName is a key in the
// format "namespace/name". If releaseName is empty, search for a matching
// release.
func (h *Helm) Delete(bundleID, releaseName string) error {
	keepResources := false
	deployments, err := h.ListDeployments()
	if err != nil {
		return err
	}
	for _, deployment := range deployments {
		if deployment.BundleID == bundleID {
			if releaseName == "" {
				releaseName = deployment.ReleaseName
			}
			keepResources = deployment.KeepResources
			break
		}
	}
	if releaseName == "" {
		// Never found anything to delete
		return nil
	}
	return h.deleteByRelease(bundleID, releaseName, keepResources)
}

func (h *Helm) deleteByRelease(bundleID, releaseName string, keepResources bool) error {
	releaseNamespace, releaseName := kv.Split(releaseName, "/")
	rels, err := h.globalCfg.Releases.List(func(r *release.Release) bool {
		return r.Namespace == releaseNamespace &&
			r.Name == releaseName &&
			r.Chart.Metadata.Annotations[BundleIDAnnotation] == bundleID &&
			r.Chart.Metadata.Annotations[AgentNamespaceAnnotation] == h.agentNamespace
	})
	if err != nil {
		return nil
	}
	if len(rels) == 0 {
		return nil
	}

	var (
		serviceAccountName string
	)
	for _, rel := range rels {
		serviceAccountName = rel.Chart.Metadata.Annotations[ServiceAccountNameAnnotation]
		if serviceAccountName != "" {
			break
		}
	}

	cfg, err := h.getCfg(releaseNamespace, serviceAccountName)
	if err != nil {
		return err
	}

	if strings.HasPrefix(bundleID, "fleet-agent") {
		// Never uninstall the fleet-agent, just "forget" it
		return deleteHistory(cfg, bundleID)
	}

	if keepResources {
		// don't delete resources, just delete the helm release secrets
		return deleteHistory(cfg, bundleID)
	}

	u := action.NewUninstall(&cfg)
	_, err = u.Run(releaseName)
	return err
}

func (h *Helm) delete(bundleID string, options fleet.BundleDeploymentOptions, dryRun bool) error {
	timeout, _, releaseName := h.getOpts(bundleID, options)

	r, err := h.globalCfg.Releases.Last(releaseName)
	if err != nil {
		return nil
	}

	if r.Chart.Metadata.Annotations[BundleIDAnnotation] != bundleID {
		rels, err := h.globalCfg.Releases.History(releaseName)
		if err != nil {
			return nil
		}
		r = nil
		for _, rel := range rels {
			if rel.Chart.Metadata.Annotations[BundleIDAnnotation] == bundleID {
				r = rel
				break
			}
		}
		if r == nil {
			return fmt.Errorf("failed to find helm release to delete for %s", bundleID)
		}
	}

	serviceAccountName := r.Chart.Metadata.Annotations[ServiceAccountNameAnnotation]
	cfg, err := h.getCfg(r.Namespace, serviceAccountName)
	if err != nil {
		return err
	}

	if strings.HasPrefix(bundleID, "fleet-agent") {
		// Never uninstall the fleet-agent, just "forget" it
		return deleteHistory(cfg, bundleID)
	}

	u := action.NewUninstall(&cfg)
	u.DryRun = dryRun
	u.Timeout = timeout

	if !dryRun {
		logrus.Infof("Helm: Uninstalling %s", bundleID)
	}
	_, err = u.Run(releaseName)
	return err
}

func (h *Helm) createCfg(namespace string) (action.Configuration, error) {
	kc := kube.New(h.getter)
	kc.Log = logrus.Infof
	clientSet, err := kc.Factory.KubernetesClientSet()
	if err != nil {
		return action.Configuration{}, err
	}
	driver := driver.NewSecrets(helmcache.NewSecretClient(h.secretCache, clientSet, namespace))
	driver.Log = logrus.Infof
	store := storage.Init(driver)
	store.MaxHistory = MaxHelmHistory

	return action.Configuration{
		RESTClientGetter: h.getter,
		Releases:         store,
		KubeClient:       kc,
		Log:              logrus.Infof,
	}, nil
}

func deleteHistory(cfg action.Configuration, bundleID string) error {
	releases, err := cfg.Releases.List(func(r *release.Release) bool {
		return r.Name == bundleID && r.Chart.Metadata.Annotations[BundleIDAnnotation] == bundleID
	})
	if err != nil {
		return err
	}
	for _, release := range releases {
		logrus.Infof("Helm: Deleting release %s %d", release.Name, release.Version)
		if _, err := cfg.Releases.Delete(release.Name, release.Version); err != nil {
			return err
		}
	}
	return nil
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
	if err := yaml.Unmarshal(values, &m); err != nil {
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
	if err := yaml.Unmarshal([]byte(values), &m); err != nil {
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

func releaseToResources(release *release.Release) (*Resources, error) {
	var (
		err error
	)
	resources := &Resources{
		DefaultNamespace: release.Namespace,
		ID:               fmt.Sprintf("%s/%s:%d", release.Namespace, release.Name, release.Version),
	}

	resources.Objects, err = yaml.ToObjects(bytes.NewBufferString(release.Manifest))
	return resources, err
}

// GetSetID constructs a identifier from the provided args, bundleID "fleet-agent" is special
func GetSetID(bundleID, labelPrefix, labelSuffix string) string {
	// bundle is fleet-agent bundle, we need to use setID fleet-agent-bootstrap since it was applied with import controller
	if strings.HasPrefix(bundleID, "fleet-agent") {
		if labelSuffix == "" {
			return config.AgentBootstrapConfigName
		}
		return name.SafeConcatName(config.AgentBootstrapConfigName, labelSuffix)
	}
	if labelSuffix != "" {
		return name.SafeConcatName(labelPrefix, bundleID, labelSuffix)
	}
	return name.SafeConcatName(labelPrefix, bundleID)
}

func removeFailedRollback(cfg action.Configuration, currentRelease *release.Release, err error) error {
	failedRelease, errRel := cfg.Releases.Last(currentRelease.Name)
	if errRel != nil {
		return errors.Wrap(err, errRel.Error())
	}
	if failedRelease.Version == currentRelease.Version+1 &&
		failedRelease.Info.Status == release.StatusFailed &&
		strings.HasPrefix(failedRelease.Info.Description, "Rollback") {
		_, errDel := cfg.Releases.Delete(failedRelease.Name, failedRelease.Version)
		if errDel != nil {
			return errors.Wrap(err, errDel.Error())
		}
		errUpdate := cfg.Releases.Update(currentRelease)
		if errUpdate != nil {
			return errors.Wrap(err, errUpdate.Error())
		}
	}

	return err
}

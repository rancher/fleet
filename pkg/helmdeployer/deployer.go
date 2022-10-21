package helmdeployer

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/kustomize"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/rawyaml"
	"github.com/rancher/fleet/pkg/render"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kv"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/yaml"
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
	useGlobalCfg        bool
	template            bool
	defaultNamespace    string
	labelPrefix         string
	labelSuffix         string
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
	if err := h.globalCfg.Init(getter, "", "secrets", logrus.Infof); err != nil {
		return nil, err
	}
	h.globalCfg.Releases.MaxHistory = 5
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

	newObjs, processed, err := kustomize.Process(p.manifest, data, p.opts.Kustomize.Dir)
	if err != nil {
		return nil, err
	}
	if processed {
		objs = newObjs
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

	tar, err := render.ToChart(bundleID, manifest, options)
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
	if manifest.Commit != "" {
		chart.Metadata.Annotations[CommitAnnotation] = manifest.Commit
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

	if options.TargetNamespace != "" {
		options.DefaultNamespace = options.TargetNamespace
	}

	if options.DefaultNamespace == "" {
		options.DefaultNamespace = h.defaultNamespace
	}

	// releaseName has a limit of 53 in helm https://github.com/helm/helm/blob/main/pkg/action/install.go#L58
	// apply already makes sure that the bundle.Name is not longer than 53 characters
	releaseName := name.Limit(bundleID, 53)
	if options.Helm != nil && options.Helm.ReleaseName != "" {
		// JSON schema validation makes sure that the option is valid
		releaseName = options.Helm.ReleaseName
	}

	return timeout, options.DefaultNamespace, releaseName
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

	err = cfg.Init(getter, namespace, "secrets", logrus.Infof)
	cfg.Releases.MaxHistory = 5
	cfg.KubeClient = kClient

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
		u.ForceAdopt = options.Helm.TakeOwnership
		u.Replace = true
		u.ReleaseName = releaseName
		u.CreateNamespace = true
		u.Namespace = defaultNamespace
		u.Timeout = timeout
		u.DryRun = dryRun
		u.PostRenderer = pr
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
	u.Force = options.Helm.Force
	u.Atomic = options.Helm.Atomic
	u.MaxHistory = options.Helm.MaxHistory
	if u.MaxHistory == 0 {
		u.MaxHistory = 10
	}
	u.Namespace = defaultNamespace
	u.Timeout = timeout
	u.DryRun = dryRun
	u.DisableOpenAPIValidation = h.template || dryRun
	u.PostRenderer = pr
	if u.Timeout > 0 {
		u.Wait = true
	}
	if !dryRun {
		logrus.Infof("Helm: Upgrading %s", bundleID)
	}
	return u.Run(releaseName, chart, values)
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
				tempValues, err = processValuesFromObject(name, namespace, key, secret, nil)
				if err != nil {
					return nil, err
				}
			} else if valuesFrom.ConfigMapKeyRef != nil {
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
				tempValues, err = processValuesFromObject(name, namespace, key, nil, configMap)
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

// ListDeployments returns a list of bundles by listing all helm relases via
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
		result = append(result, DeployedBundle{
			BundleID:    d,
			ReleaseName: release.Namespace + "/" + release.Name,
		})
	}

	return result, nil
}

func (h *Helm) getRelease(bundleID, resourcesID string) (*release.Release, error) {

	// When a bundle is installed a resourcesID is generated. If there is no
	// resourcesID then there isn't anything to lookup.
	if resourcesID == "" {
		return nil, ErrNoResourceID
	}

	hist := action.NewHistory(&h.globalCfg)

	namespace, name := kv.Split(resourcesID, "/")
	releaseName, versionStr := kv.Split(name, ":")
	version, _ := strconv.Atoi(versionStr)

	if releaseName == "" {
		releaseName = bundleID
	}

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
	if _, err := h.getRelease(bundleID, resourcesID); err == ErrNoRelease {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

func (h *Helm) Resources(bundleID, resourcesID string) (*Resources, error) {
	release, err := h.getRelease(bundleID, resourcesID)
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
	if releaseName == "" {
		deployments, err := h.ListDeployments()
		if err != nil {
			return err
		}
		for _, deployment := range deployments {
			if deployment.BundleID == bundleID {
				releaseName = deployment.ReleaseName
				break
			}
		}
	}
	if releaseName == "" {
		// Never found anything to delete
		return nil
	}
	return h.deleteByRelease(bundleID, releaseName)
}

func (h *Helm) deleteByRelease(bundleID, releaseName string) error {
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

func processValuesFromObject(name, namespace, key string, secret *corev1.Secret, configMap *corev1.ConfigMap) (map[string]interface{}, error) {
	var m map[string]interface{}
	if secret != nil {
		values, ok := secret.Data[key]
		if !ok {
			return nil, fmt.Errorf("key %s is missing from secret %s/%s, can't use it in valuesFrom", key, namespace, name)
		}
		if err := yaml.Unmarshal(values, &m); err != nil {
			return nil, err
		}
	} else if configMap != nil {
		values, ok := configMap.Data[key]
		if !ok {
			return nil, fmt.Errorf("key %s is missing from configmap %s/%s, can't use it in valuesFrom", key, namespace, name)
		}
		if err := yaml.Unmarshal([]byte(values), &m); err != nil {
			return nil, err
		}
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

// mergeValues merges source and destination map, preferring values
// from the source values. This is slightly adapted from:
// https://github.com/helm/helm/blob/2332b480c9cb70a0d8a85247992d6155fbe82416/cmd/helm/install.go#L359
func mergeValues(dest, src map[string]interface{}) map[string]interface{} {
	for k, v := range src {
		// If the key doesn't exist already, then just set the key to that value
		if _, exists := dest[k]; !exists {
			dest[k] = v
			continue
		}
		nextMap, ok := v.(map[string]interface{})
		// If it isn't another map, overwrite the value
		if !ok {
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
			return "fleet-agent-bootstrap"
		}
		return name.SafeConcatName("fleet-agent-bootstrap", labelSuffix)
	}
	if labelSuffix != "" {
		return name.SafeConcatName(labelPrefix, bundleID, labelSuffix)
	}
	return name.SafeConcatName(labelPrefix, bundleID)
}

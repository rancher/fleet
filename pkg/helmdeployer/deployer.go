package helmdeployer

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/kustomize"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/render"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kv"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const (
	BundleIDAnnotation           = "fleet.cattle.io/bundle-id"
	ServiceAccountNameAnnotation = "fleet.cattle.io/service-account"
	DefaultServiceAccount        = "fleetDefault"
)

type helm struct {
	serviceAccountNamespace string
	serviceAccountCache     corecontrollers.ServiceAccountCache
	getter                  genericclioptions.RESTClientGetter
	globalCfg               action.Configuration
	useGlobalCfg            bool
	template                bool
	defaultNamespace        string
	labelPrefix             string
}

func NewHelm(namespace, defaultNamespace, labelPrefix string, getter genericclioptions.RESTClientGetter,
	serviceAccountCache corecontrollers.ServiceAccountCache) (deployer.Deployer, error) {
	h := &helm{
		getter:                  getter,
		defaultNamespace:        defaultNamespace,
		serviceAccountNamespace: namespace,
		serviceAccountCache:     serviceAccountCache,
		labelPrefix:             labelPrefix,
	}
	if err := h.globalCfg.Init(getter, "", "secrets", logrus.Infof); err != nil {
		return nil, err
	}
	h.globalCfg.Releases.MaxHistory = 5
	return h, nil
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

type postRender struct {
	labelPrefix string
	bundleID    string
	manifest    *manifest.Manifest
	opts        fleet.BundleDeploymentOptions
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

	newObjs, processed, err := kustomize.Process(p.manifest, data, p.opts.KustomizeDir)
	if err != nil {
		return nil, err
	}
	if processed {
		objs = newObjs
	}

	labels, annotations, err := apply.GetLabelsAndAnnotations(name.SafeConcatName(p.labelPrefix, p.bundleID), nil)
	if err != nil {
		return nil, err
	}

	for _, obj := range objs {
		meta, err := meta.Accessor(obj)
		if err != nil {
			return nil, err
		}
		meta.SetLabels(mergeMaps(meta.GetLabels(), labels))
		meta.SetAnnotations(mergeMaps(meta.GetAnnotations(), annotations))
	}

	data, err = yaml.ToBytes(objs)
	return bytes.NewBuffer(data), err
}

func (h *helm) Deploy(bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions) (*deployer.Resources, error) {
	tar, err := render.ToChart(bundleID, manifest)
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

func (h *helm) mustUninstall(cfg *action.Configuration, bundleID string) (bool, error) {
	r, err := cfg.Releases.Last(bundleID)
	if err != nil {
		return false, nil
	}
	return r.Info.Status == release.StatusUninstalling, err
}

func (h *helm) mustInstall(cfg *action.Configuration, bundleID string) (bool, error) {
	_, err := cfg.Releases.Deployed(bundleID)
	if err != nil && strings.Contains(err.Error(), "has no deployed releases") {
		return true, nil
	}
	return false, err
}

func (h *helm) getOpts(options fleet.BundleDeploymentOptions) (map[string]interface{}, time.Duration, string) {
	vals := map[string]interface{}{}
	if options.Values != nil {
		vals = options.Values.Data
	}

	timeout := 10 * time.Minute
	if options.TimeoutSeconds > 0 {
		timeout = time.Second * time.Duration(options.TimeoutSeconds)
	}

	if options.DefaultNamespace == "" {
		options.DefaultNamespace = h.defaultNamespace
	}

	return vals, timeout, options.DefaultNamespace
}

func (h *helm) getCfg(namespace, serviceAccountName string) (action.Configuration, error) {
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

	err = cfg.Init(getter, namespace, "secrets", logrus.Infof)
	cfg.Releases.MaxHistory = 5

	return cfg, err
}

func (h *helm) install(bundleID string, manifest *manifest.Manifest, chart *chart.Chart, options fleet.BundleDeploymentOptions, dryRun bool) (*release.Release, error) {
	vals, timeout, namespace := h.getOpts(options)

	cfg, err := h.getCfg(namespace, options.ServiceAccount)
	if err != nil {
		return nil, err
	}

	uninstall, err := h.mustUninstall(&cfg, bundleID)
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

	install, err := h.mustInstall(&cfg, bundleID)
	if err != nil {
		return nil, err
	}

	pr := &postRender{
		labelPrefix: h.labelPrefix,
		bundleID:    bundleID,
		manifest:    manifest,
		opts:        options,
	}

	if install {
		u := action.NewInstall(&cfg)
		u.ClientOnly = h.template
		u.ForceAdopt = true
		u.Replace = true
		u.Wait = true
		u.ReleaseName = bundleID
		u.CreateNamespace = true
		u.Namespace = namespace
		u.Timeout = timeout
		u.DryRun = dryRun
		u.PostRenderer = pr
		return u.Run(chart, vals)
	}

	u := action.NewUpgrade(&cfg)
	u.Adopt = true
	u.Force = options.Force
	u.Namespace = namespace
	u.Timeout = timeout
	u.Atomic = true
	u.DryRun = dryRun
	u.PostRenderer = pr
	return u.Run(bundleID, chart, vals)
}

func (h *helm) ListDeployments() ([]string, error) {
	list := action.NewList(&h.globalCfg)
	list.All = true
	releases, err := list.Run()
	if err != nil {
		return nil, err
	}

	var (
		seen   = map[string]bool{}
		result []string
	)

	for _, release := range releases {
		d := release.Chart.Metadata.Annotations["fleet.cattle.io/bundle-id"]
		if d != "" && !seen[d] {
			result = append(result, d)
			seen[d] = true
		}
	}

	return result, nil
}

func (h *helm) Resources(deploymentID, resourcesID string) (*deployer.Resources, error) {
	hist := action.NewHistory(&h.globalCfg)

	releases, err := hist.Run(deploymentID)
	if err != nil {
		return nil, err
	}

	namespace, name := kv.Split(resourcesID, "/")
	releaseName, versionStr := kv.Split(name, ":")
	version, _ := strconv.Atoi(versionStr)

	for _, release := range releases {
		if release.Name == releaseName && release.Version == version && release.Namespace == namespace {
			return releaseToResources(release)
		}
	}

	return &deployer.Resources{}, nil
}

func (h *helm) Delete(bundleID string) error {
	return h.delete(bundleID, fleet.BundleDeploymentOptions{}, false)
}

func (h *helm) delete(bundleID string, options fleet.BundleDeploymentOptions, dryRun bool) error {
	_, timeout, _ := h.getOpts(options)

	r, err := h.globalCfg.Releases.Last(bundleID)
	if err != nil {
		return nil
	}

	if r.Chart.Metadata.Annotations[BundleIDAnnotation] != bundleID {
		rels, err := h.globalCfg.Releases.History(bundleID)
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

	u := action.NewUninstall(&cfg)
	u.DryRun = dryRun
	u.Timeout = timeout

	_, err = u.Run(bundleID)
	return err
}

func releaseToResources(release *release.Release) (*deployer.Resources, error) {
	var (
		err error
	)
	resources := &deployer.Resources{
		DefaultNamespace: release.Namespace,
		ID:               fmt.Sprintf("%s/%s:%d", release.Namespace, release.Name, release.Version),
	}

	resources.Objects, err = yaml.ToObjects(bytes.NewBufferString(release.Manifest))
	return resources, err
}

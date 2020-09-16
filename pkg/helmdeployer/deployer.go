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
	"helm.sh/helm/v3/pkg/kube"
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

	newObjs, processed, err := kustomize.Process(p.manifest, data, p.opts.Kustomize.Dir)
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

func (h *helm) mustUninstall(cfg *action.Configuration, releaseName string) (bool, error) {
	r, err := cfg.Releases.Last(releaseName)
	if err != nil {
		return false, nil
	}
	return r.Info.Status == release.StatusUninstalling, err
}

func (h *helm) mustInstall(cfg *action.Configuration, releaseName string) (bool, error) {
	_, err := cfg.Releases.Deployed(releaseName)
	if err != nil && strings.Contains(err.Error(), "has no deployed releases") {
		return true, nil
	}
	return false, err
}

func (h *helm) getOpts(bundleID string, options fleet.BundleDeploymentOptions) (map[string]interface{}, time.Duration, string, string) {
	if options.Helm == nil {
		options.Helm = &fleet.HelmOptions{}
	}

	vals := map[string]interface{}{}
	if options.Helm.Values != nil {
		vals = options.Helm.Values.Data
	}

	var timeout time.Duration
	if options.Helm.TimeoutSeconds > 0 {
		timeout = time.Second * time.Duration(options.Helm.TimeoutSeconds)
	}

	if options.DefaultNamespace == "" {
		options.DefaultNamespace = h.defaultNamespace
	}

	releaseName := bundleID
	if options.Helm != nil && options.Helm.ReleaseName != "" {
		releaseName = options.Helm.ReleaseName
	}

	return vals, timeout, options.DefaultNamespace, releaseName
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

	kClient := kube.New(getter)
	kClient.Namespace = namespace

	err = cfg.Init(getter, namespace, "secrets", logrus.Infof)
	cfg.Releases.MaxHistory = 5
	cfg.KubeClient = kClient

	return cfg, err
}

func (h *helm) install(bundleID string, manifest *manifest.Manifest, chart *chart.Chart, options fleet.BundleDeploymentOptions, dryRun bool) (*release.Release, error) {
	vals, timeout, namespace, releaseName := h.getOpts(bundleID, options)

	cfg, err := h.getCfg(namespace, options.ServiceAccount)
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
		bundleID:    bundleID,
		manifest:    manifest,
		opts:        options,
	}

	if install {
		u := action.NewInstall(&cfg)
		u.ClientOnly = h.template
		u.ForceAdopt = options.Helm.TakeOwnership
		u.Replace = true
		u.ReleaseName = releaseName
		u.CreateNamespace = true
		u.Namespace = namespace
		u.Timeout = timeout
		u.DryRun = dryRun
		u.PostRenderer = pr
		if u.Timeout > 0 {
			u.Wait = true
		}
		if !dryRun {
			logrus.Infof("Helm: Installing %s", bundleID)
		}
		return u.Run(chart, vals)
	}

	u := action.NewUpgrade(&cfg)
	u.Adopt = true
	u.Force = options.Helm.Force
	u.Namespace = namespace
	u.Timeout = timeout
	u.Atomic = true
	u.DryRun = dryRun
	u.PostRenderer = pr
	if u.Timeout > 0 {
		u.Wait = true
	}
	if !dryRun {
		logrus.Infof("Helm: Upgrading %s", bundleID)
	}
	return u.Run(releaseName, chart, vals)
}

func (h *helm) ListDeployments() ([]deployer.DeployedBundle, error) {
	list := action.NewList(&h.globalCfg)
	list.All = true
	releases, err := list.Run()
	if err != nil {
		return nil, err
	}

	var (
		result []deployer.DeployedBundle
	)

	for _, release := range releases {
		d := release.Chart.Metadata.Annotations["fleet.cattle.io/bundle-id"]
		if d == "" {
			continue
		}
		result = append(result, deployer.DeployedBundle{
			BundleID:    d,
			ReleaseName: release.Namespace + "/" + release.Name,
		})
	}

	return result, nil
}

func (h *helm) Resources(bundleID, resourcesID string) (*deployer.Resources, error) {
	hist := action.NewHistory(&h.globalCfg)

	namespace, name := kv.Split(resourcesID, "/")
	releaseName, versionStr := kv.Split(name, ":")
	version, _ := strconv.Atoi(versionStr)

	if releaseName == "" {
		releaseName = bundleID
	}

	releases, err := hist.Run(releaseName)
	if err != nil {
		return nil, err
	}

	for _, release := range releases {
		if release.Name == releaseName && release.Version == version && release.Namespace == namespace {
			return releaseToResources(release)
		}
	}

	return &deployer.Resources{}, nil
}

func (h *helm) Delete(bundleID, releaseName string) error {
	if releaseName != "" {
		return h.deleteByRelease(bundleID, releaseName)
	}
	return h.delete(bundleID, fleet.BundleDeploymentOptions{}, false)
}

func (h *helm) deleteByRelease(bundleID, releaseName string) error {
	releaseNamespace, releaseName := kv.Split(releaseName, "/")
	rels, err := h.globalCfg.Releases.List(func(r *release.Release) bool {
		return r.Namespace == releaseNamespace &&
			r.Name == releaseName &&
			r.Chart.Metadata.Annotations[BundleIDAnnotation] == bundleID
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

	u := action.NewUninstall(&cfg)
	_, err = u.Run(releaseName)
	return err
}

func (h *helm) delete(bundleID string, options fleet.BundleDeploymentOptions, dryRun bool) error {
	_, timeout, _, releaseName := h.getOpts(bundleID, options)

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

	if bundleID == "fleet-agent" {
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

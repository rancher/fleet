package helmdeployer

import (
	"context"
	"fmt"
	"io"

	"github.com/Masterminds/semver/v3"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"

	"k8s.io/apimachinery/pkg/runtime"
)

var (
	defaultKubernetesVersion = "v1.25.0"
)

// Template runs helm template and returns the resources as a list of objects, without applying them.
func Template(ctx context.Context, bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions, kubeVersionString string) ([]runtime.Object, error) {
	h := &Helm{
		globalCfg:    action.Configuration{},
		useGlobalCfg: true,
		template:     true,
	}

	mem := driver.NewMemory()
	mem.SetNamespace("default")
	kubeVersionToUse := defaultKubernetesVersion
	if kubeVersionString != "" {
		kubeVersionToUse = kubeVersionString
	}
	kubeVersion, err := semver.NewVersion(kubeVersionToUse)
	if err != nil {
		return nil, fmt.Errorf("invalid kubeVersion: %s", kubeVersionToUse)
	}
	h.globalCfg.Capabilities = chartutil.DefaultCapabilities.Copy()
	h.globalCfg.Capabilities.KubeVersion = chartutil.KubeVersion{
		Version: kubeVersion.String(),
		Major:   fmt.Sprint(kubeVersion.Major()),
		Minor:   fmt.Sprint(kubeVersion.Minor()),
	}
	h.globalCfg.KubeClient = &kubefake.PrintingKubeClient{Out: io.Discard}
	h.globalCfg.Log = logrus.Infof
	h.globalCfg.Releases = storage.Init(mem)

	resources, err := h.Deploy(ctx, bundleID, manifest, options)
	if err != nil {
		return nil, err
	}

	return ReleaseToObjects(resources)
}

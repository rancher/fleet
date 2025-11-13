package helmdeployer

import (
	"context"
	"fmt"
	"io"

	"github.com/Masterminds/semver/v3"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
)

var (
	defaultKubernetesVersion = "v1.25.0"
)

// Template runs helm template and returns the resources as a list of objects, without applying them.
func Template(ctx context.Context, bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions, kubeVersionString string) (*releasev1.Release, error) {
	h := &Helm{
		globalCfg:    &action.Configuration{},
		useGlobalCfg: true,
		template:     true,
	}

	mem := driver.NewMemory()
	mem.SetNamespace("default")
	// Template operations use a discard logger since they don't interact with a real cluster
	mem.SetLogger(nil) // nil sets discard handler in Helm v4
	kubeVersionToUse := defaultKubernetesVersion
	if kubeVersionString != "" {
		kubeVersionToUse = kubeVersionString
	}
	kubeVersion, err := semver.NewVersion(kubeVersionToUse)
	if err != nil {
		return nil, fmt.Errorf("invalid kubeVersion: %s", kubeVersionToUse)
	}
	h.globalCfg.Capabilities = common.DefaultCapabilities.Copy()
	h.globalCfg.Capabilities.KubeVersion = common.KubeVersion{
		Version: kubeVersion.String(),
		Major:   fmt.Sprint(kubeVersion.Major()),
		Minor:   fmt.Sprint(kubeVersion.Minor()),
	}
	h.globalCfg.KubeClient = &kubefake.PrintingKubeClient{Out: io.Discard}
	h.globalCfg.Releases = storage.Init(mem)
	// Template operations don't need logging since they're just rendering
	h.globalCfg.SetLogger(nil) // nil sets discard handler in Helm v4

	return h.Deploy(ctx, bundleID, manifest, options)
}

package helmdeployer

import (
	"context"
	"io"

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

// Template runs helm template and returns the resources as a list of objects, without applying them.
func Template(ctx context.Context, bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions) ([]runtime.Object, error) {
	h := &Helm{
		globalCfg:    action.Configuration{},
		useGlobalCfg: true,
		template:     true,
	}

	mem := driver.NewMemory()
	mem.SetNamespace("default")

	h.globalCfg.Capabilities = chartutil.DefaultCapabilities
	h.globalCfg.KubeClient = &kubefake.PrintingKubeClient{Out: io.Discard}
	h.globalCfg.Log = logrus.Infof
	h.globalCfg.Releases = storage.Init(mem)

	resources, err := h.Deploy(ctx, bundleID, manifest, options)
	if err != nil {
		return nil, err
	}

	return resources.Objects, nil
}

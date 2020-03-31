package helmdeployer

import (
	"io/ioutil"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/runtime"
)

func Template(bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions) ([]runtime.Object, error) {
	h := &helm{
		cfg:      action.Configuration{},
		template: true,
	}

	mem := driver.NewMemory()
	mem.SetNamespace("default")

	h.cfg.Capabilities = chartutil.DefaultCapabilities
	h.cfg.KubeClient = &kubefake.PrintingKubeClient{Out: ioutil.Discard}
	h.cfg.Log = logrus.Infof
	h.cfg.Releases = storage.Init(mem)

	resources, err := h.Deploy(bundleID, manifest, options)
	if err != nil {
		return nil, err
	}

	return resources.Objects, nil
}

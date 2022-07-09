package helmdeployer

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/runtime"
)

func Template(bundleID string, manifest *manifest.Manifest, options fleet.BundleDeploymentOptions, clusterCapabilities fleet.Capabilities) ([]runtime.Object, error) {
	h := &helm{
		globalCfg:    action.Configuration{},
		useGlobalCfg: true,
		template:     true,
	}

	mem := driver.NewMemory()
	mem.SetNamespace("default")

	h.globalCfg.Capabilities = clusterCapabilities.ToHelmCapabilities()
	h.globalCfg.KubeClient = &kubefake.PrintingKubeClient{Out: ioutil.Discard}
	h.globalCfg.Log = logrus.Infof
	h.globalCfg.Releases = storage.Init(mem)

	resources, err := h.Deploy(bundleID, manifest, options)
	if err != nil {
		return nil, err
	}
	return resources.Objects, nil
}

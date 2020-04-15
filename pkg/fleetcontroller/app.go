package fleetcontroller

import (
	"context"
	"io"

	"github.com/rancher/fleet/pkg/controllers"
	"github.com/rancher/fleet/pkg/crd"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/yaml"
)

func OutputCRDs(writer io.Writer) error {
	objs, err := crd.Objects()
	if err != nil {
		return err
	}

	content, err := yaml.Export(objs...)
	if err != nil {
		return err
	}

	_, err = writer.Write(content)
	return err
}

func Start(ctx context.Context, systemNamespace string, kubeconfigFile string) error {
	clientConfig, err := kubeconfig.GetNonInteractiveClientConfig(kubeconfigFile).ClientConfig()
	if err != nil {
		return err
	}

	if err := crd.Create(ctx, clientConfig); err != nil {
		return err
	}

	return controllers.Register(ctx, systemNamespace, clientConfig)
}

package fleetcontroller

import (
	"context"
	"github.com/rancher/fleet/pkg/controllers"
	"github.com/rancher/fleet/pkg/crd"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/rancher/wrangler/pkg/yaml"
	"io"
	"k8s.io/client-go/discovery"
	"strconv"
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

func Start(ctx context.Context, systemNamespace string, kubeconfigFile string, disableGitops bool) error {
	cfg := kubeconfig.GetNonInteractiveClientConfig(kubeconfigFile)
	clientConfig, err := cfg.ClientConfig()
	if err != nil {
		return err
	}

	clientConfig.RateLimiter = ratelimit.None

	client, err := discovery.NewDiscoveryClientForConfig(clientConfig)
	if err != nil {
		return err
	}

	version, err := client.ServerVersion()
	if err != nil {
		return err
	}

	major, err  := strconv.Atoi(version.Major)
	if err != nil {
		return err
	}
	minor, err := strconv.Atoi(version.Minor)
	if err != nil {
		return err
	}
	if major < 1 || (major == 1 && minor <= 15) {
		if err := crd.CreateV1Beta1(ctx, clientConfig); err != nil {
			return err
		}
	} else {
		if err := crd.Create(ctx, clientConfig); err != nil {
			return err
		}
	}

	return controllers.Register(ctx, systemNamespace, cfg, disableGitops)
}

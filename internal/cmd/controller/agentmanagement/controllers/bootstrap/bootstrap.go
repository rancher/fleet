package bootstrap

import (
	"context"
	"os"
	"regexp"

	"github.com/pkg/errors"
	"github.com/rancher/fleet/internal/config"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	FleetBootstrap = "fleet-controller-bootstrap"
)

var (
	splitter          = regexp.MustCompile(`\s*,\s*`)
	ErrNoHostInConfig = errors.New("failed to find cluster server parameter")
)

// Register registers the bootstrap handler using controller-runtime
func Register(ctx context.Context,
	mgr ctrl.Manager,
	systemNamespace string,
	cfg clientcmd.ClientConfig,
) error {
	handler := &BootstrapHandler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		SystemNamespace: systemNamespace,
		ClientConfig:    cfg,
	}

	// Register as a config change callback
	config.OnChange(ctx, func(cfg *config.Config) error {
		return handler.OnConfig(ctx, cfg)
	})

	return nil
}

// Helper functions for kubeconfig building

func getHost(rawConfig clientcmdapi.Config) (string, error) {
	icc, err := rest.InClusterConfig()
	if err == nil {
		return icc.Host, nil
	}

	cluster, ok := rawConfig.Clusters[rawConfig.CurrentContext]
	if ok {
		return cluster.Server, nil
	}

	for _, v := range rawConfig.Clusters {
		return v.Server, nil
	}

	return "", ErrNoHostInConfig
}

func getCA(rawConfig clientcmdapi.Config) ([]byte, error) {
	icc, err := rest.InClusterConfig()
	if err == nil {
		return os.ReadFile(icc.CAFile)
	}

	cluster, ok := rawConfig.Clusters[rawConfig.CurrentContext]
	if !ok {
		for _, v := range rawConfig.Clusters {
			cluster = v
			break
		}
	}

	if cluster != nil {
		if len(cluster.CertificateAuthorityData) > 0 {
			return cluster.CertificateAuthorityData, nil
		}
		return os.ReadFile(cluster.CertificateAuthority)
	}

	return nil, nil
}

func buildKubeConfig(host string, ca []byte, token string, rawConfig clientcmdapi.Config) ([]byte, error) {
	if token == "" {
		return clientcmd.Write(rawConfig)
	}
	return clientcmd.Write(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {
				Server:                   host,
				CertificateAuthorityData: ca,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Token: token,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default": {
				Cluster:  "cluster",
				AuthInfo: "user",
			},
		},
		CurrentContext: "default",
	})
}

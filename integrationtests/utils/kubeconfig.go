package utils

import (
	"fmt"
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

func WriteKubeConfig(cfg *rest.Config, path string) error {
	config := FromEnvTestConfig(cfg)
	if err := os.WriteFile(path, config, 0600); err != nil {
		return err
	}
	return nil
}

// FromEnvTestConfig returns a new Kubeconfig in byte form when running in envtest.
func FromEnvTestConfig(cfg *rest.Config) []byte {
	name := "testenv-cluster"
	contextName := fmt.Sprintf("%s@%s", cfg.Username, name)
	c := api.Config{
		Clusters: map[string]*api.Cluster{
			name: {
				Server:                   cfg.Host,
				CertificateAuthorityData: cfg.CAData,
			},
		},
		Contexts: map[string]*api.Context{
			contextName: {
				Cluster:  name,
				AuthInfo: cfg.Username,
			},
		},
		AuthInfos: map[string]*api.AuthInfo{
			cfg.Username: {
				ClientKeyData:         cfg.KeyData,
				ClientCertificateData: cfg.CertData,
			},
		},
		CurrentContext: contextName,
	}
	data, _ := clientcmd.Write(c)
	return data
}

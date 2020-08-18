package bootstrap

import (
	"context"
	"io/ioutil"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/wrangler/pkg/apply"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
)

type handler struct {
	apply apply.Apply
	cfg   clientcmd.ClientConfig
}

func Register(ctx context.Context, apply apply.Apply, cfg clientcmd.ClientConfig) {
	h := handler{
		apply: apply.WithSetID("fleet-bootstrap"),
		cfg:   cfg,
	}
	config.OnChange(ctx, h.OnConfig)
}

func (h *handler) OnConfig(config *config.Config) error {
	var objs []runtime.Object

	if config.BootstrapNamespace == "" {
		return nil
	}

	secret, err := getSecret(config.BootstrapNamespace, h.cfg)
	if err != nil {
		return err
	}

	objs = append(objs, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: config.BootstrapNamespace,
		},
	}, secret, &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local",
			Namespace: config.BootstrapNamespace,
			Labels: map[string]string{
				"name": "local",
			},
		},
		Spec: fleet.ClusterSpec{
			KubeConfigSecret: secret.Name,
		},
	})

	if config.BootstrapRepo != "" {
		objs = append(objs, &fleet.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bootstrap",
				Namespace: config.BootstrapNamespace,
			},
			Spec: fleet.GitRepoSpec{
				Repo:   config.BootstrapRepo,
				Branch: config.BootstrapBranch,
			},
		})
	}

	return h.apply.ApplyObjects(objs...)
}

func getHost(cfg clientcmd.ClientConfig) (string, error) {
	rawConfig, err := cfg.RawConfig()
	if err != nil {
		return "", err
	}

	cluster, ok := rawConfig.Clusters[rawConfig.CurrentContext]
	if !ok {
		for _, v := range rawConfig.Clusters {
			return v.Server, nil
		}
	}
	return cluster.Server, nil
}

func getCA(cfg clientcmd.ClientConfig) ([]byte, error) {
	rawConfig, err := cfg.RawConfig()
	if err != nil {
		return nil, err
	}

	cluster, ok := rawConfig.Clusters[rawConfig.CurrentContext]
	if !ok {
		for _, v := range rawConfig.Clusters {
			cluster = v
			break
		}
	}

	if len(cluster.CertificateAuthorityData) > 0 {
		return cluster.CertificateAuthorityData, nil
	}
	return ioutil.ReadFile(cluster.CertificateAuthority)
}

func getSecret(bootstrapNamespace string, cfg clientcmd.ClientConfig) (*corev1.Secret, error) {
	rawConfig, err := cfg.RawConfig()
	if err != nil {
		return nil, err
	}

	value, err := clientcmd.Write(rawConfig)
	if err != nil {
		return nil, err
	}

	host, err := getHost(cfg)
	if err != nil {
		return nil, err
	}

	ca, err := getCA(cfg)
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-cluster",
			Namespace: bootstrapNamespace,
		},
		Data: map[string][]byte{
			"value":        value,
			"apiServerURL": []byte(host),
			"apiServerCA":  ca,
		},
	}, nil

}

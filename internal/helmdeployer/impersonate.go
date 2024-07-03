package helmdeployer

import (
	"context"
	"fmt"

	"github.com/rancher/wrangler/v3/pkg/ratelimit"

	corev1 "k8s.io/api/core/v1"
	apierror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func (h *Helm) getServiceAccount(ctx context.Context, name string) (string, string, error) {
	currentName := name
	if currentName == "" {
		currentName = DefaultServiceAccount
	}
	sa := &corev1.ServiceAccount{}
	err := h.client.Get(ctx, types.NamespacedName{Namespace: h.agentNamespace, Name: currentName}, sa)
	if apierror.IsNotFound(err) && name == "" {
		// if we can't find the service account, but none was asked for, don't use any
		return "", "", nil
	} else if err != nil {
		return "", "", fmt.Errorf("looking up service account %s/%s: %w", h.agentNamespace, currentName, err)
	}
	return h.agentNamespace, currentName, nil
}

type impersonatingGetter struct {
	genericclioptions.RESTClientGetter

	config     clientcmd.ClientConfig
	restConfig *rest.Config
}

func newImpersonatingGetter(namespace, name string, getter genericclioptions.RESTClientGetter) (genericclioptions.RESTClientGetter, error) {
	config := clientcmd.NewDefaultClientConfig(impersonationConfig(namespace, name), &clientcmd.ConfigOverrides{})

	restConfig, err := config.ClientConfig()
	if err != nil {
		return nil, err
	}
	restConfig.RateLimiter = ratelimit.None

	return &impersonatingGetter{
		RESTClientGetter: getter,
		config:           config,
		restConfig:       restConfig,
	}, nil
}

func (i *impersonatingGetter) ToRESTConfig() (*rest.Config, error) {
	return i.restConfig, nil
}

func (i *impersonatingGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return i.config
}

func impersonationConfig(namespace, name string) clientcmdapi.Config {
	return clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {
				Server:               "https://kubernetes.default",
				CertificateAuthority: "/run/secrets/kubernetes.io/serviceaccount/ca.crt",
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				TokenFile:   "/run/secrets/kubernetes.io/serviceaccount/token",
				Impersonate: fmt.Sprintf("system:serviceaccount:%s:%s", namespace, name),
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default": {
				Cluster:  "cluster",
				AuthInfo: "user",
			},
		},
		CurrentContext: "default",
	}
}

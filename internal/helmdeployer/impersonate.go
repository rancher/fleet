package helmdeployer

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// getServiceAccount is called with an empty name, unless the user specified a
// service account in their git repo.
//
// If the service account passed to this func is empty, it will default to
// "fleet-default". It will then check for existence and return the default
// account or an empty namespace and name.
// Returning an empty name, will make the agent use its own service account,
// which is cluster admin.
//
// If a specific account was passed in and it was not found, it will return an error.
func (h *Helm) getServiceAccount(ctx context.Context, name string) (string, string, error) {
	currentName := name
	if currentName == "" {
		currentName = DefaultServiceAccount
	}
	sa := &corev1.ServiceAccount{}
	err := h.client.Get(ctx, types.NamespacedName{Namespace: h.agentNamespace, Name: currentName}, sa)
	if apierror.IsNotFound(err) && name == "" {
		// if we can't find the fleet-default service account, but none
		// was asked for, use the pods service account instead
		return "", "", nil
	} else if err != nil {
		// we failed to find an explicitly asked for service account
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
	restConfig.QPS = -1
	restConfig.RateLimiter = nil

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

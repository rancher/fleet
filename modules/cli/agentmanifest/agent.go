package agentmanifest

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rancher/fleet/modules/cli/agentconfig"
	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/fleet/pkg/agent"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	fleetns "github.com/rancher/fleet/pkg/namespace"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/yaml"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var (
	ErrNoHostInConfig = errors.New("failed to find cluster server parameter")
)

type Options struct {
	CA               []byte
	Host             string
	NoCA             bool
	Labels           map[string]string
	ClientID         string
	Generation       string
	CheckinInterval  string
	AgentEnvVars     []v1.EnvVar
	AgentTolerations []v1.Toleration
}

func AgentToken(ctx context.Context, agentNamespace, controllerNamespace string, client *client.Client, tokenName string, opts *Options) ([]runtime.Object, error) {
	token, err := getToken(ctx, controllerNamespace, tokenName, client)
	if err != nil {
		return nil, err
	}

	if opts.Host != "" {
		token["apiServerURL"] = []byte(opts.Host)
	}
	if len(opts.CA) > 0 {
		token["apiServerCA"] = opts.CA
	}

	return objects(agentNamespace, token), nil
}

func insecurePing(host string) {
	// I do this to make k3s generate a new SAN if it needs to
	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Get(host)
	if err == nil {
		resp.Body.Close()
	}
}

func AgentManifest(ctx context.Context, agentNamespace, controllerNamespace, agentScope string, cg *client.Getter, output io.Writer, tokenName string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	client, err := cg.Get()
	if err != nil {
		return err
	}

	objs, err := AgentToken(ctx, agentNamespace, controllerNamespace, client, tokenName, opts)
	if err != nil {
		return err
	}

	agentConfig, err := agentconfig.AgentConfig(ctx, agentNamespace, controllerNamespace, cg, &agentconfig.Options{
		Labels:   opts.Labels,
		ClientID: opts.ClientID,
	})
	if err != nil {
		return err
	}

	objs = append(objs, agentConfig...)

	cfg, err := config.Lookup(ctx, controllerNamespace, config.ManagerConfigName, client.Core.ConfigMap())
	if err != nil {
		return err
	}

	objs = append(objs, agent.Manifest(agentNamespace, agentScope, cfg.AgentImage, cfg.AgentImagePullPolicy, opts.Generation, opts.CheckinInterval, opts.AgentEnvVars, opts.AgentTolerations)...)

	data, err := yaml.Export(objs...)
	if err != nil {
		return err
	}

	_, err = output.Write(data)
	return err
}

func checkHost(host string) error {
	u, err := url.Parse(host)
	if err != nil {
		return errors.Wrapf(err, "invalid host, override with --server-url")
	}
	if u.Hostname() == "localhost" || strings.HasPrefix(u.Hostname(), "127.") || u.Hostname() == "0.0.0.0" {
		return fmt.Errorf("invalid host %s in server URL, use --server-url to set a proper server URL for the kubernetes endpoint", u.Hostname())
	}
	return nil
}

func getKubeConfig(kubeConfig string, namespace string, token []byte, host string, ca []byte, noCA bool) (string, error) {
	cc := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	cfg, err := cc.RawConfig()
	if err != nil {
		return "", err
	}

	customHost := len(host) > 0

	host, doCheckHost, err := getHost(host, cfg)
	if err != nil {
		return "", err
	}

	if doCheckHost {
		if err := checkHost(host); err != nil {
			return "", err
		}
	}

	if noCA {
		ca = nil
	} else if !customHost {
		ca, err = getCA(ca, cfg)
		if err != nil {
			return "", err
		}
	}

	cfg = clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {
				Server:                   host,
				CertificateAuthorityData: ca,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Token: string(token),
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default": {
				Cluster:   "cluster",
				AuthInfo:  "user",
				Namespace: namespace,
			},
		},
		CurrentContext: "default",
	}

	data, err := clientcmd.Write(cfg)
	return string(data), err
}

func getHost(host string, cfg clientcmdapi.Config) (string, bool, error) {
	if host != "" {
		return host, false, nil
	}

	host, err := GetHostFromConfig(cfg)
	if err != nil {
		return "", false, err
	}

	return host, true, nil
}

func getCA(ca []byte, cfg clientcmdapi.Config) ([]byte, error) {
	if len(ca) > 0 {
		return ca, nil
	}

	return GetCAFromConfig(cfg)
}

func getToken(ctx context.Context, controllerNamespace, tokenName string, client *client.Client) (map[string][]byte, error) {
	secretName, err := waitForSecretName(ctx, tokenName, client)
	if err != nil {
		return nil, err
	}

	secret, err := client.Core.Secret().Get(client.Namespace, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	values := secret.Data["values"]
	if len(values) == 0 {
		return nil, fmt.Errorf("failed to find \"values\" on secret %s/%s", client.Namespace, secretName)
	}

	data := map[string]interface{}{}
	if err := yaml.Unmarshal(values, &data); err != nil {
		return nil, err
	}

	if _, ok := data["token"]; !ok {
		return nil, fmt.Errorf("failed to find token in values")
	}

	expectedNamespace := fleetns.RegistrationNamespace(controllerNamespace)
	actualNamespace := data["systemRegistrationNamespace"]
	if actualNamespace != expectedNamespace {
		return nil, fmt.Errorf("registration namespace (%s) from secret (%s/%s) does not match expected: %s", actualNamespace, secret.Namespace, secret.Name, expectedNamespace)
	}

	byteData := map[string][]byte{}
	for k, v := range data {
		if s, ok := v.(string); ok {
			byteData[k] = []byte(s)
		}
	}

	return byteData, nil
}

func waitForSecretName(ctx context.Context, tokenName string, client *client.Client) (string, error) {
	watcher, err := startWatch(client.Namespace, client.Fleet.ClusterRegistrationToken())
	if err != nil {
		return "", err
	}
	defer func() {
		watcher.Stop()
		for range watcher.ResultChan() {
			// drain the channel
		}
	}()

	crt, err := client.Fleet.ClusterRegistrationToken().Get(client.Namespace, tokenName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to lookup token %s: %w", tokenName, err)
	}
	if crt.Status.SecretName != "" {
		return crt.Status.SecretName, nil
	}

	timeout := time.After(time.Minute)
	for {
		var event watch.Event
		select {
		case <-timeout:
			return "", fmt.Errorf("timeout getting credential for cluster group")
		case <-ctx.Done():
			return "", ctx.Err()
		case event = <-watcher.ResultChan():
		}

		if newCGT, ok := event.Object.(*fleet.ClusterRegistrationToken); ok {
			if newCGT.UID != crt.UID || newCGT.Status.SecretName == "" {
				continue
			}
			return newCGT.Status.SecretName, nil
		}
	}
}

func startWatch(namespace string, sa fleetcontrollers.ClusterRegistrationTokenClient) (watch.Interface, error) {
	secrets, err := sa.List(namespace, metav1.ListOptions{
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	return sa.Watch(namespace, metav1.ListOptions{ResourceVersion: secrets.ResourceVersion})
}

func GetCAFromConfig(rawConfig clientcmdapi.Config) ([]byte, error) {
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
		return ioutil.ReadFile(cluster.CertificateAuthority)
	}

	return nil, nil
}

func GetHostFromConfig(rawConfig clientcmdapi.Config) (string, error) {
	cluster, ok := rawConfig.Clusters[rawConfig.CurrentContext]
	if ok {
		return cluster.Server, nil
	}

	for _, v := range rawConfig.Clusters {
		return v.Server, nil
	}

	return "", ErrNoHostInConfig
}

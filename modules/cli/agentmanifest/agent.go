package agentmanifest

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/rancher/fleet/pkg/agent"

	"github.com/pkg/errors"
	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/yaml"
	coreV1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type Options struct {
	TTL       time.Duration
	CA        []byte
	Host      string
	NoCA      bool
	TokenOnly bool
}

func AgentToken(ctx context.Context, controllerNamespace, clusterGroupName, kubeConfigFile string, client *client.Client, opts *Options) ([]runtime.Object, error) {
	clusterGroup, err := getClusterGroup(ctx, clusterGroupName, client)
	if err != nil {
		return nil, err
	}

	token, err := getToken(ctx, clusterGroup, opts.TTL, client)
	if err != nil {
		return nil, err
	}

	kubeConfig, err := getKubeConfig(kubeConfigFile, clusterGroup.Status.Namespace, token, opts.Host, opts.CA, opts.NoCA)
	if err != nil {
		return nil, err
	}

	return objects(controllerNamespace, kubeConfig), nil
}

func AgentManifest(ctx context.Context, controllerNamespace, clusterGroupName string, cg *client.Getter, output io.Writer, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	client, err := cg.Get()
	if err != nil {
		return err
	}

	objs, err := AgentToken(ctx, controllerNamespace, clusterGroupName, cg.Kubeconfig, client, opts)
	if err != nil {
		return err
	}

	cfg, err := config.Lookup(ctx, controllerNamespace, config.ManagerConfigName, client.Core.ConfigMap())
	if err != nil {
		return err
	}

	if !opts.TokenOnly {
		objs = append(objs, agent.Manifest(controllerNamespace, cfg.AgentImage)...)
	}

	data, err := yaml.Export(objs...)
	if err != nil {
		return err
	}

	_, err = output.Write(data)
	return err
}

func getClusterGroup(ctx context.Context, clusterGroupName string, client *client.Client) (*fleet.ClusterGroup, error) {
	timeout := time.After(2 * time.Minute)
	for {
		clusterGroup, err := client.Fleet.ClusterGroup().Get(client.Namespace, clusterGroupName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			clusterGroup, err = client.Fleet.ClusterGroup().Create(&fleet.ClusterGroup{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: client.Namespace,
					Name:      clusterGroupName,
				},
			})
		}
		if err != nil {
			return nil, errors.Wrapf(err, "invalid cluster group %s/%s", client.Namespace, clusterGroupName)
		}

		if clusterGroup.Status.Namespace != "" {
			return clusterGroup, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for cluster group %s/%s to be assigned a namespace", client.Namespace, clusterGroupName)
		case <-time.After(time.Second):
		}
	}
}

func checkHost(host string) error {
	u, err := url.Parse(host)
	if err != nil {
		return errors.Wrapf(err, "invalid host, override with --server-url")
	}
	if u.Hostname() == "localhost" || strings.HasPrefix(u.Hostname(), "127.") {
		return fmt.Errorf("invalid host %s, use --server-url to set a proper server URL", u.Hostname())
	}
	return nil
}

func getKubeConfig(kubeConfig string, namespace, token, host string, ca []byte, noCA bool) (string, error) {
	cc := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	cfg, err := cc.RawConfig()
	if err != nil {
		return "", err
	}

	host, err = getHost(host, cfg)
	if err != nil {
		return "", err
	}

	if err := checkHost(host); err != nil {
		return "", err
	}

	if noCA {
		ca = nil
	} else {
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
				Token: token,
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

func getCluster(cfg clientcmdapi.Config) (*clientcmdapi.Cluster, error) {
	ctx := cfg.Contexts[cfg.CurrentContext]
	if ctx == nil {
		return nil, fmt.Errorf("failed to find host for agent access, context not found")
	}

	cluster := cfg.Clusters[ctx.Cluster]
	if cluster == nil {
		return nil, fmt.Errorf("failed to find host for agent access, cluster not found")
	}

	return cluster, nil
}

func getHost(host string, cfg clientcmdapi.Config) (string, error) {
	if host != "" {
		return host, nil
	}

	cluster, err := getCluster(cfg)
	if err != nil {
		return "", err
	}

	return cluster.Server, nil
}

func getCA(ca []byte, cfg clientcmdapi.Config) ([]byte, error) {
	if len(ca) > 0 {
		return ca, nil
	}

	cluster, err := getCluster(cfg)
	if err != nil {
		return nil, err
	}

	if len(cluster.CertificateAuthorityData) > 0 {
		return cluster.CertificateAuthorityData, nil
	}

	if cluster.CertificateAuthority != "" {
		return ioutil.ReadFile(cluster.CertificateAuthority)
	}

	return nil, nil
}

func getToken(ctx context.Context, clusterGroup *fleet.ClusterGroup, ttl time.Duration, client *client.Client) (string, error) {
	watcher, err := startWatch(clusterGroup.Namespace, client.Fleet.ClusterGroupToken())
	if err != nil {
		return "", err
	}
	defer func() {
		watcher.Stop()
		for range watcher.ResultChan() {
			// drain the channel
		}
	}()

	cgt, err := client.Fleet.ClusterGroupToken().Create(&fleet.ClusterGroupToken{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    clusterGroup.Namespace,
			GenerateName: "token-",
		},
		Spec: fleet.ClusterGroupTokenSpec{
			TTLSeconds:       int(ttl / time.Second),
			ClusterGroupName: clusterGroup.Name,
		},
	})
	if err != nil {
		return "", err
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

		if newCGT, ok := event.Object.(*fleet.ClusterGroupToken); ok {
			if newCGT.UID != cgt.UID || newCGT.Status.SecretName == "" {
				continue
			}
			secret, err := client.Core.Secret().Get(clusterGroup.Namespace, newCGT.Status.SecretName, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			token := secret.Data[coreV1.ServiceAccountTokenKey]
			if len(token) == 0 {
				return "", fmt.Errorf("failed to find token on secret %s/%s", clusterGroup.Namespace, newCGT.Status.SecretName)
			}
			return string(token), nil
		}
	}
}

func startWatch(namespace string, sa fleetcontrollers.ClusterGroupTokenClient) (watch.Interface, error) {
	secrets, err := sa.List(namespace, metav1.ListOptions{
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	return sa.Watch(namespace, metav1.ListOptions{ResourceVersion: secrets.ResourceVersion})
}

// Package agent builds manifests for creating a managed fleet-agent. (fleetcontroller)
package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pkg/errors"

	"github.com/rancher/fleet/modules/agent/pkg/register"
	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	fleetns "github.com/rancher/fleet/pkg/namespace"

	"github.com/rancher/wrangler/pkg/yaml"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var (
	ErrNoHostInConfig = errors.New("failed to find cluster server parameter")
)

type Options struct {
	ManifestOptions
	ConfigOptions
	CA   []byte
	Host string
	NoCA bool // unused
}

func agentToken(ctx context.Context, agentNamespace, controllerNamespace string, client *client.Client, tokenName string, opts *Options) ([]runtime.Object, error) {
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

	return tokenObjects(agentNamespace, token), nil
}

// AgentWithConfig writes the agent manifest to the given writer. It
// includes an updated agent token secret from the cluster. It finds or creates
// the agent config inside a configmap.
//
// This is used when importing a cluster.
func AgentWithConfig(ctx context.Context, agentNamespace, controllerNamespace, agentScope string, cg *client.Getter, output io.Writer, tokenName string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	client, err := cg.Get()
	if err != nil {
		return err
	}

	objs, err := agentToken(ctx, agentNamespace, controllerNamespace, client, tokenName, opts)
	if err != nil {
		return err
	}

	agentConfig, err := agentConfig(ctx, agentNamespace, controllerNamespace, cg, &opts.ConfigOptions)
	if err != nil {
		return err
	}

	objs = append(objs, agentConfig...)

	// get a fresh config from the API
	cfg, err := config.Lookup(ctx, controllerNamespace, config.ManagerConfigName, client.Core.ConfigMap())
	if err != nil {
		return err
	}

	mo := opts.ManifestOptions
	mo.AgentImage = cfg.AgentImage
	mo.SystemDefaultRegistry = cfg.SystemDefaultRegistry
	mo.AgentImagePullPolicy = cfg.AgentImagePullPolicy
	mo.CheckinInterval = cfg.AgentCheckinInternal.Duration.String()

	objs = append(objs, Manifest(agentNamespace, agentScope, mo)...)

	data, err := yaml.Export(objs...)
	if err != nil {
		return err
	}

	_, err = output.Write(data)
	return err
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

	timeout := time.After(durations.AgentSecretTimeout)
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
		return os.ReadFile(cluster.CertificateAuthority)
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

func tokenObjects(namespace string, data map[string][]byte) []runtime.Object {
	objs := []runtime.Object{
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
				Annotations: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
		},
		&v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      register.BootstrapCredName,
				Namespace: namespace,
			},
			Data: data,
		},
	}

	return objs
}

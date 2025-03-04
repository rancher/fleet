// Package agent builds manifests for creating a managed fleet-agent.
package agent

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/rancher/fleet/internal/client"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"
	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/apimachinery/pkg/watch"
)

type Options struct {
	ManifestOptions
	ConfigOptions
	APIServerCA  []byte
	APIServerURL string
	NoCA         bool // unused
}

// AgentWithConfig returns the agent manifest. It includes an updated agent
// token secret from the cluster. It finds or creates the agent config inside a
// configmap.
//
// This is used when importing a cluster.
func AgentWithConfig(ctx context.Context, agentNamespace, controllerNamespace, agentScope string, cg *client.Getter, tokenName string, opts *Options) ([]runtime.Object, error) {
	if opts == nil {
		opts = &Options{}
	}

	objs := []runtime.Object{
		&v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: agentNamespace}},
	}

	client, err := cg.Get()
	if err != nil {
		return objs, err
	}

	secret, err := agentBootstrapSecret(ctx, agentNamespace, controllerNamespace, client, tokenName, opts)
	if err != nil {
		return objs, err
	}

	objs = append(objs, secret)

	agentConfig, err := agentConfig(ctx, agentNamespace, controllerNamespace, cg, &opts.ConfigOptions)
	if err != nil {
		return objs, err
	}

	objs = append(objs, agentConfig...)

	// get a fresh config from the API
	cfg, err := config.Lookup(ctx, controllerNamespace, config.ManagerConfigName, client.Core.ConfigMap())
	if err != nil {
		return objs, err
	}

	// keep in sync with manageagent.go
	mo := opts.ManifestOptions
	mo.AgentImage = cfg.AgentImage
	mo.AgentImagePullPolicy = cfg.AgentImagePullPolicy
	mo.CheckinInterval = cfg.AgentCheckinInterval.Duration.String()
	mo.SystemDefaultRegistry = cfg.SystemDefaultRegistry
	mo.BundleDeploymentWorkers = cfg.AgentWorkers.BundleDeployment
	mo.DriftWorkers = cfg.AgentWorkers.Drift

	objs = append(objs, Manifest(agentNamespace, agentScope, mo)...)

	return objs, err
}

// agentBootstrapSecret creates the fleet-agent-bootstrap secret from the
// import-token-<clusterName> secret and adds the APIServer options.
func agentBootstrapSecret(ctx context.Context, agentNamespace, controllerNamespace string, client *client.Client, tokenName string, opts *Options) (*v1.Secret, error) {
	data, err := getToken(ctx, controllerNamespace, tokenName, client)
	if err != nil {
		return nil, err
	}

	if opts.APIServerURL != "" {
		data[config.APIServerURLKey] = []byte(opts.APIServerURL)
	}
	if len(opts.APIServerCA) > 0 {
		data[config.APIServerCAKey] = opts.APIServerCA
	}

	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.AgentBootstrapConfigName,
			Namespace: agentNamespace,
		},
		Data: data,
	}, nil
}

// getToken load the import-token-local secret and
// check system registration namespace is expected
func getToken(ctx context.Context, controllerNamespace, tokenName string, client *client.Client) (map[string][]byte, error) {
	secretName, err := waitForSecretName(ctx, tokenName, client)
	if err != nil {
		return nil, err
	}

	secret, err := client.Core.Secret().Get(client.Namespace, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// unmarshal kubeconfig yaml from values key
	values := secret.Data[config.ImportTokenSecretValuesKey]
	if len(values) == 0 {
		return nil, fmt.Errorf("failed to find \"values\" on secret %s/%s", client.Namespace, secretName)
	}

	data := map[string]interface{}{}
	if err := yaml.NewYAMLToJSONDecoder(bytes.NewBuffer(values)).Decode(&data); err != nil {
		return nil, err
	}

	if _, ok := data["token"]; !ok {
		return nil, fmt.Errorf("failed to find token in values")
	}

	expectedNamespace := fleetns.SystemRegistrationNamespace(controllerNamespace)
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

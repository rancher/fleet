// Package config implements the config for the fleet manager and agent
package config

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/rancher/fleet/pkg/version"

	corev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	ManagerConfigName        = "fleet-controller"
	AgentConfigName          = "fleet-agent"
	AgentBootstrapConfigName = "fleet-agent-bootstrap"
	AgentTLSModeStrict       = "strict"
	AgentTLSModeSystemStore  = "system-store"
	Key                      = "config"
	// DefaultNamespace is the default for the system namespace, which
	// contains the manager and agent
	DefaultNamespace       = "cattle-fleet-system"
	LegacyDefaultNamespace = "fleet-system"
	// ImportTokenSecretValuesKey is the key in the import token secret,
	// which contains the values for cluster registration.
	ImportTokenSecretValuesKey = "values"
	// KubeConfigSecretValueKey is the key in the kubeconfig secret, which
	// contains the kubeconfig for the downstream cluster.
	KubeConfigSecretValueKey = "value"
	// APIServerURLKey is the key which contains the API server URL of the
	// upstream server. It is used in the controller config, the kubeconfig
	// secret of a cluster, the cluster registration secret "import-NAME"
	// and the fleet-agent-bootstrap secret.
	APIServerURLKey = "apiServerURL"
	// APIServerCAKey is the key which contains the CA of the upstream
	// server.
	APIServerCAKey = "apiServerCA"
)

var (
	DefaultManagerImage = "rancher/fleet" + ":" + version.Version
	DefaultAgentImage   = "rancher/fleet-agent" + ":" + version.Version

	config       *Config
	callbacks    = map[int]func(*Config) error{}
	callbackID   int
	callbackLock sync.Mutex
)

// Config is the config for the fleet manager and agent. Each use slightly
// different fields from this struct. It is stored as JSON in configmaps under
// the 'config' key.
type Config struct {
	// AgentImage defaults to rancher/fleet-agent:version if empty, can include a prefixed SystemDefaultRegistry
	AgentImage           string `json:"agentImage,omitempty"`
	AgentImagePullPolicy string `json:"agentImagePullPolicy,omitempty"`

	// SystemDefaultRegistry used by Rancher when constructing the
	// agentImage string, it's in the config so fleet can remove it if a
	// private repo url prefix is specified on the agent's cluster resource
	SystemDefaultRegistry string `json:"systemDefaultRegistry,omitempty"`

	// AgentCheckinInterval determines how often agents update their clusters status, defaults to 15m
	AgentCheckinInterval metav1.Duration `json:"agentCheckinInterval,omitempty"`

	// ManageAgent if present and set to false, no bundles will be created to manage agents
	ManageAgent *bool `json:"manageAgent,omitempty"`

	// Labels are copied to the cluster registration resource. In detail:
	// fleet-controller will copy the labels to the fleet-agent's config,
	// fleet-agent copies the labels to the cluster registration resource,
	// when fleet-controller accepts the registration, the labels are
	// copied to the cluster resource.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// ClientID of the cluster to associate with. Used by the agent only.
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// APIServerURL is the URL of the fleet-controller's k8s API server. It
	// can be empty, if the value is provided in the cluster's kubeconfig
	// secret instead. The value is copied into the fleet-agent-bootstrap
	// secret on the downstream cluster.
	// +optional
	APIServerURL string `json:"apiServerURL,omitempty"`

	// APIServerCA is the CA bundle used to connect to the
	// fleet-controllers k8s API server. It can be empty, if the value is
	// provided in the cluster's kubeconfig secret instead. The value is
	// copied into the fleet-agent-bootstrap secret on the downstream
	// cluster.
	// +optional
	APIServerCA []byte `json:"apiServerCA,omitempty"`

	Bootstrap Bootstrap `json:"bootstrap,omitempty"`

	// IgnoreClusterRegistrationLabels if set to true, the labels on the cluster registration resource will not be copied to the cluster resource.
	IgnoreClusterRegistrationLabels bool `json:"ignoreClusterRegistrationLabels,omitempty"`

	// AgentTLSMode supports two values: `system-store` and `strict`. If set to `system-store`, instructs the agent
	// to trust CA bundles from the operating system's store. If set to `strict`, then the agent shall only connect
	// to a server which uses the exact CA configured when creating/updating the agent.
	AgentTLSMode string `json:"agentTLSMode,omitempty"`

	// The amount of time to wait for a response from the server before
	// canceling the request.  Used to retrieve the latest commit of configured
	// git repositories.
	GitClientTimeout metav1.Duration `json:"gitClientTimeout,omitempty"`
}

type Bootstrap struct {
	Namespace      string `json:"namespace,omitempty"`
	AgentNamespace string `json:"agentNamespace,omitempty"`
	// Repo to add at install time that will deploy to the local cluster. This allows
	// one to fully bootstrap fleet, its configuration and all its downstream clusters
	// in one shot.
	Repo   string `json:"repo,omitempty"`
	Secret string `json:"secret,omitempty"` // gitrepo.ClientSecretName for agent from repo
	Paths  string `json:"paths,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// OnChange is used by agentmanagement to react to config changes. The callback is triggered by 'Set' via
// the config controller during startup and when the configmap changes.
func OnChange(ctx context.Context, f func(*Config) error) {
	callbackLock.Lock()
	defer callbackLock.Unlock()

	callbackID++
	id := callbackID
	callbacks[id] = f

	go func() {
		<-ctx.Done()
		callbackLock.Lock()
		delete(callbacks, id)
		callbackLock.Unlock()
	}()
}

// Set doesn't trigger the callbacks, use SetAndTrigger for that. Set is used
// by controller-runtime controllers.
func Set(cfg *Config) {
	config = cfg
}

// SetAndTrigger sets the config and triggers the callbacks. It is used by the
// agentmanagement wrangler controllers.
func SetAndTrigger(cfg *Config) error {
	callbackLock.Lock()
	defer callbackLock.Unlock()

	config = cfg
	for _, f := range callbacks {
		if err := f(cfg); err != nil {
			return err
		}
	}
	return nil
}

func Get() *Config {
	if config == nil {
		panic("config.Get() called before Set()")
	}
	return config
}

func Exists(_ context.Context, namespace, name string, configMaps corev1.ConfigMapClient) (bool, error) {
	_, err := configMaps.Get(namespace, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

func Lookup(_ context.Context, namespace, name string, configMaps corev1.ConfigMapClient) (*Config, error) {
	cm, err := configMaps.Get(namespace, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		cm = &v1.ConfigMap{}
	} else if err != nil {
		return nil, err
	}

	return ReadConfig(cm)
}

func DefaultConfig() *Config {
	return &Config{
		AgentImage: DefaultAgentImage,
	}
}

func ReadConfig(cm *v1.ConfigMap) (*Config, error) {
	cfg := DefaultConfig()
	data := cm.Data[Key]
	if len(data) == 0 {
		return cfg, nil
	}

	err := yaml.Unmarshal([]byte(data), &cfg)
	return cfg, err
}

func ToConfigMap(namespace, name string, cfg *Config) (*v1.ConfigMap, error) {
	bytes, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			Key: string(bytes),
		},
	}, nil
}

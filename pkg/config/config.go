package config

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/rancher/fleet/pkg/version"

	corev1 "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	ManagerConfigName        = "fleet-controller"
	AgentConfigName          = "fleet-agent"
	AgentBootstrapConfigName = "fleet-agent-bootstrap"
	Key                      = "config"
	DefaultNamespace         = "cattle-fleet-system"
)

var (
	DefaultManagerImage        = "rancher/fleet" + ":" + version.Version
	DefaultAgentImage          = "rancher/fleet-agent" + ":" + version.Version
	DefaultAgentSimulatorImage = "rancher/fleet-agent-simulator" + ":" + version.Version

	config       *Config
	callbacks    = map[int]func(*Config) error{}
	callbackID   int
	callbackLock sync.Mutex
)

type Config struct {
	AgentImage                      string            `json:"agentImage,omitempty"`
	AgentImagePullPolicy            string            `json:"agentImagePullPolicy,omitempty"`
	SystemDefaultRegistry           string            `json:"systemDefaultRegistry,omitempty"`
	AgentCheckinInternal            metav1.Duration   `json:"agentCheckinInternal,omitempty"`
	ManageAgent                     *bool             `json:"manageAgent,omitempty"`
	Labels                          map[string]string `json:"labels,omitempty"`
	ClientID                        string            `json:"clientID,omitempty"`
	APIServerURL                    string            `json:"apiServerURL,omitempty"`
	APIServerCA                     []byte            `json:"apiServerCA,omitempty"`
	Bootstrap                       Bootstrap         `json:"bootstrap,omitempty"`
	IgnoreClusterRegistrationLabels bool              `json:"ignoreClusterRegistrationLabels,omitempty"`
}

type Bootstrap struct {
	Namespace      string `json:"namespace,omitempty"`
	AgentNamespace string `json:"agentNamespace,omitempty"`
	Repo           string `json:"repo,omitempty"`
	Secret         string `json:"secret,omitempty"`
	Paths          string `json:"paths,omitempty"`
	Branch         string `json:"branch,omitempty"`
}

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

func Set(cfg *Config) error {
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

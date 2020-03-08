package config

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/rancher/fleet/pkg/version"
	corev1 "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	Name      = "fleet"
	AgentName = "fleet-agent"
	Namespace = "fleet-system"
)

var (
	config       *Config
	callbacks    = map[int]func(*Config) error{}
	callbackID   int
	callbackLock sync.Mutex
)

type Config struct {
	AgentImage         string            `json:"agentImage,omitempty"`
	ManageAgent        *bool             `json:"manageAgent,omitempty"`
	InitialDataVersion int               `json:"initialDataVersion,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
}

func Store(cfg *Config, namespace string, configMaps corev1.ConfigMapClient) error {
	newCM, err := ToConfigMap(namespace, Name, cfg)
	if err != nil {
		return err
	}

	cm, err := configMaps.Get(namespace, Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = configMaps.Create(newCM)
		return err
	}

	cm.Data = newCM.Data
	_, err = configMaps.Update(cm)
	if err != nil {
		return err
	}

	return Set(cfg)
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

	return ReadConfig(name, cm)
}

func DefaultConfig() *Config {
	return &Config{
		AgentImage: "rancher/fleet-agent:" + version.Version,
	}
}

func ReadConfig(name string, cm *v1.ConfigMap) (*Config, error) {
	cfg := DefaultConfig()
	data := cm.Data[name]
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
			name: string(bytes),
		},
	}, nil
}

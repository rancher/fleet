package register

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	"github.com/rancher/fleet/pkg/registration"

	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/randomtoken"
	"github.com/rancher/wrangler/pkg/ratelimit"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/yaml"
)

const (
	CredName            = "fleet-agent"
	BootstrapCredName   = "fleet-agent-bootstrap" // same as config.AgentBootstrapConfigName for fleet-controller
	Kubeconfig          = "kubeconfig"
	Token               = "token"
	Values              = "values"
	APIServerURL        = "apiServerURL"
	APIServerCA         = "apiServerCA"
	DeploymentNamespace = "deploymentNamespace"
	ClusterNamespace    = "clusterNamespace"
	ClusterName         = "clusterName"
)

type AgentInfo struct {
	ClusterNamespace string
	ClusterName      string
	ClientConfig     clientcmd.ClientConfig
}

func Register(ctx context.Context, namespace, clusterID string, config *rest.Config) (*AgentInfo, error) {
	for {
		cfg, err := tryRegister(ctx, namespace, clusterID, config)
		if err == nil {
			return cfg, nil
		}
		logrus.Errorf("Failed to register agent: %v", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(durations.AgentRegistrationRetry):
		}
	}
}

// tryRegister makes sure the secret cattle-fleet-system/fleet-agent is
// populated and the contained kubeconfig is working
func tryRegister(ctx context.Context, namespace, clusterID string, config *rest.Config) (*AgentInfo, error) {
	config = rest.CopyConfig(config)
	config.RateLimiter = ratelimit.None
	k8s, err := core.NewFactoryFromConfig(config)
	if err != nil {
		return nil, err
	}

	secret, err := k8s.Core().V1().Secret().Get(namespace, CredName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// fallback to local cattle-fleet-system/fleet-agent-bootstrap
		secret, err = runRegistration(ctx, k8s.Core().V1(), namespace, clusterID)
		if err != nil {
			return nil, fmt.Errorf("looking up secret %s/%s: %w", namespace, BootstrapCredName, err)
		}
	} else if err != nil {
		return nil, err
	} else if err := testClientConfig(secret.Data[Kubeconfig]); err != nil {
		// skip testClientConfig check if previous error, or IsNotFound fallback succeeded
		logrus.Errorf("Current credential failed, failing back to reregistering: %v", err)
		secret, err = runRegistration(ctx, k8s.Core().V1(), namespace, clusterID)
		if err != nil {
			return nil, fmt.Errorf("looking up secret %s/%s or %s/%s: %w", namespace, BootstrapCredName, namespace, CredName, err)
		}
	}

	clientConfig, err := clientcmd.NewClientConfigFromBytes(secret.Data[Kubeconfig])
	if err != nil {
		return nil, err
	}

	// delete the bootstrap cred
	_ = k8s.Core().V1().Secret().Delete(namespace, BootstrapCredName, nil)
	return &AgentInfo{
		ClusterNamespace: string(secret.Data[ClusterNamespace]),
		ClusterName:      string(secret.Data[ClusterName]),
		ClientConfig:     clientConfig,
	}, nil
}

func runRegistration(ctx context.Context, k8s corecontrollers.Interface, namespace, clusterID string) (*corev1.Secret, error) {
	// read cattle-fleet-system/fleet-agent-bootstrap
	secret, err := k8s.Secret().Get(namespace, BootstrapCredName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("looking up secret %s/%s: %w", namespace, BootstrapCredName, err)
	}
	return createClusterSecret(ctx, clusterID, k8s, secret)
}

// createClusterSecret uses the provided fleet-agent-bootstrap token to build a
// kubeconfig and create a ClusterRegistration.
// Then goes into a loop, waiting for the registration secret "clientID" to
// appear in the systemRegistrationNamespace.
// Finally uses the client from the config (service account: fleet-agent), to
// update the "fleet-agent" secret from the registration secret.
func createClusterSecret(ctx context.Context, clusterID string, k8s corecontrollers.Interface, secret *corev1.Secret) (*corev1.Secret, error) {
	clientConfig := createClientConfigFromSecret(secret)

	ns, _, err := clientConfig.Namespace()
	if err != nil {
		return nil, err
	}

	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	cfg, err := config.Lookup(ctx, secret.Namespace, config.AgentConfigName, k8s.ConfigMap())
	if err != nil {
		return nil, err
	}

	fleetK8s, err := kubernetes.NewForConfig(kc)
	if err != nil {
		return nil, err
	}

	fc, err := fleetcontrollers.NewFactoryFromConfig(kc)
	if err != nil {
		return nil, err
	}

	token, err := randomtoken.Generate()
	if err != nil {
		return nil, err
	}

	if cfg.ClientID != "" {
		clusterID = cfg.ClientID
	} else if clusterID == "" {
		kubeSystem, err := k8s.Namespace().Get("kube-system", metav1.GetOptions{})
		if err != nil {
			return nil, err
		}

		clusterID = string(kubeSystem.UID)
	}

	request, err := fc.Fleet().V1alpha1().ClusterRegistration().Create(&fleet.ClusterRegistration{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "request-",
			Namespace:    ns,
		},
		Spec: fleet.ClusterRegistrationSpec{
			ClientID:      clusterID,
			ClientRandom:  token,
			ClusterLabels: cfg.Labels,
		},
	})
	if err != nil {
		return nil, err
	}

	secretName := registration.SecretName(request.Spec.ClientID, request.Spec.ClientRandom)
	secretNamespace := string(values(secret.Data)["systemRegistrationNamespace"])
	timeout := time.After(durations.CreateClusterSecretTimeout)

	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for secret %s/%s", secretNamespace, secretName)
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(durations.ClusterSecretRetry):
		}

		newSecret, err := fleetK8s.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			logrus.Infof("Waiting for secret %s/%s for %s/%s: %v", secretNamespace, secretName, request.Namespace, request.Name, err)
			continue
		}

		newToken := newSecret.Data[Token]
		clusterNamespace := newSecret.Data[ClusterNamespace]
		clusterName := newSecret.Data[ClusterName]
		deploymentNamespace := newSecret.Data[DeploymentNamespace]

		newKubeconfig, err := updateClientConfig(clientConfig, string(newToken), string(deploymentNamespace))
		if err != nil {
			return nil, err
		}

		if err := testClientConfig(newKubeconfig); err != nil {
			return nil, err
		}

		updatedSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CredName,
				Namespace: secret.Namespace,
			},
			Data: map[string][]byte{
				Kubeconfig:          newKubeconfig,
				DeploymentNamespace: deploymentNamespace,
				ClusterNamespace:    clusterNamespace,
				ClusterName:         clusterName,
			},
		}

		secret, err := k8s.Secret().Create(updatedSecret)
		if apierrors.IsAlreadyExists(err) {
			if err = k8s.Secret().Delete(updatedSecret.Namespace, updatedSecret.Name, &metav1.DeleteOptions{}); err != nil {
				return nil, err
			}
			secret, err = k8s.Secret().Create(updatedSecret)
		}
		return secret, err
	}
}

func values(data map[string][]byte) map[string][]byte {
	values := data[Values]
	if len(values) == 0 {
		return data
	}

	newData := map[string]interface{}{}
	if err := yaml.Unmarshal(values, &newData); err != nil {
		return data
	}

	data = map[string][]byte{}
	for k, v := range newData {
		if s, ok := v.(string); ok {
			data[k] = []byte(s)
		}
	}
	return data
}

func createClientConfigFromSecret(secret *corev1.Secret) clientcmd.ClientConfig {
	data := values(secret.Data)
	apiServerURL := string(data[APIServerURL])
	apiServerCA := data[APIServerCA]
	namespace := string(data[ClusterNamespace])
	token := string(data[Token])

	if _, err := http.Get(apiServerURL); err == nil {
		apiServerCA = nil
	}

	cfg := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {
				Server:                   apiServerURL,
				CertificateAuthorityData: apiServerCA,
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

	return clientcmd.NewDefaultClientConfig(cfg, &clientcmd.ConfigOverrides{})
}

func testClientConfig(cfg []byte) error {
	cc, err := clientcmd.NewClientConfigFromBytes(cfg)
	if err != nil {
		return err
	}

	ns, _, err := cc.Namespace()
	if err != nil {
		return err
	}

	rest, err := cc.ClientConfig()
	if err != nil {
		return err
	}

	fc, err := fleetcontrollers.NewFactoryFromConfig(rest)
	if err != nil {
		return err
	}

	_, err = fc.Fleet().V1alpha1().BundleDeployment().List(ns, metav1.ListOptions{})
	return err
}

func updateClientConfig(cc clientcmd.ClientConfig, token, ns string) ([]byte, error) {
	raw, err := cc.RawConfig()
	if err != nil {
		return nil, err
	}
	for _, v := range raw.AuthInfos {
		v.Token = token
	}
	for _, v := range raw.Contexts {
		v.Namespace = ns
	}

	return clientcmd.Write(raw)
}

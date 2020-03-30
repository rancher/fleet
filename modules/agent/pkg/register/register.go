package register

import (
	"context"
	"fmt"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	fleetv1alpha1 "github.com/rancher/fleet/pkg/generated/clientset/versioned/typed/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/registration"
	"github.com/rancher/wrangler-api/pkg/generated/controllers/core"
	corev1 "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/randomtoken"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	CredName   = "fleet-agent"
	Kubeconfig = "kubeconfig"
	Token      = "token"
	Namespace  = "namespace"
)

func Register(ctx context.Context, namespace string, config *rest.Config) (clientcmd.ClientConfig, error) {
	for {
		cfg, err := tryRegister(ctx, namespace, config)
		if err == nil {
			return cfg, nil
		}
		logrus.Errorf("Failed to register agent: %v", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Minute):
		}
	}
}

func tryRegister(ctx context.Context, namespace string, config *rest.Config) (clientcmd.ClientConfig, error) {
	k8s, err := core.NewFactoryFromConfig(config)
	if err != nil {
		return nil, err
	}

	secret, err := k8s.Core().V1().Secret().Get(namespace, CredName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("no credential found")
	} else if err != nil {
		return nil, err
	}

	if secret.Annotations[fleet.BootstrapToken] == "true" {
		secret, err = createClusterSecret(ctx, k8s.Core().V1(), secret)
		if err != nil {
			return nil, err
		}
	}

	return clientcmd.NewClientConfigFromBytes(secret.Data[Kubeconfig])
}

func createClusterSecret(ctx context.Context, k8s corev1.Interface, secret *v1.Secret) (*v1.Secret, error) {
	clientConfig, err := clientcmd.NewClientConfigFromBytes(secret.Data[Kubeconfig])
	if err != nil {
		return nil, err
	}

	ns, _, err := clientConfig.Namespace()
	if err != nil {
		return nil, err
	}

	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	kubeSystem, err := k8s.Namespace().Get("kube-system", metav1.GetOptions{})
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

	fc, err := fleetv1alpha1.NewForConfig(kc)
	if err != nil {
		return nil, err
	}

	token, err := randomtoken.Generate()
	if err != nil {
		return nil, err
	}

	request, err := fc.ClusterRegistrationRequests(ns).Create(ctx, &fleet.ClusterRegistrationRequest{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "request-",
			Namespace:    ns,
		},
		Spec: fleet.ClusterRegistrationRequestSpec{
			ClientID:      string(kubeSystem.UID),
			ClientRandom:  token,
			ClusterLabels: cfg.Labels,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	secretName := registration.SecretName(request.Spec.ClientID, request.Spec.ClientRandom)
	timeout := time.After(5 * time.Minute)

	for {
		time.Sleep(time.Second)
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for secret %s/%s", ns, secretName)
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}

		newSecret, err := fleetK8s.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			logrus.Infof("Waiting for secret %s/%s: %v", ns, secretName, err)
			continue
		}

		newToken, newNS := newSecret.Data[Token], newSecret.Data[Namespace]
		newKubeconfig, err := updateClientConfig(clientConfig, string(newToken), string(newNS))
		if err != nil {
			return nil, err
		}

		if err := testClientConfig(ctx, newKubeconfig); err != nil {
			return nil, err
		}

		updatedSecret := secret.DeepCopy()
		updatedSecret.Data[Kubeconfig] = newKubeconfig
		delete(updatedSecret.Annotations, fleet.BootstrapToken)

		return k8s.Secret().Update(updatedSecret)
	}
}

func testClientConfig(ctx context.Context, cfg []byte) error {
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

	fc, err := fleetv1alpha1.NewForConfig(rest)
	if err != nil {
		return err
	}

	_, err = fc.BundleDeployments(ns).List(ctx, metav1.ListOptions{})
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

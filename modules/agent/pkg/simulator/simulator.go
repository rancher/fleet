package simulator

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/fleet/modules/agent/pkg/agent"
	"github.com/rancher/fleet/modules/agent/pkg/register"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func Simulate(ctx context.Context, count int, kubeConfig, namespace, defaultNamespace string) error {
	logrus.Infof("Starting %d simulators", count)

	eg, ctx := errgroup.WithContext(ctx)
	for i := 0; i < count; i++ {
		i := i
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
		logrus.Infof("STARING %s%05d", namespace, i)
		eg.Go(func() error {
			defer sem.Release(1)
			return simulateAgent(ctx, i, kubeConfig, namespace, defaultNamespace)
		})
	}

	eg.Go(func() error {
		// wait forever unless one of the simulators dies
		<-ctx.Done()
		return ctx.Err()
	})

	return eg.Wait()
}

var (
	sem = semaphore.NewWeighted(50)
)

func simulateAgent(ctx context.Context, i int, kubeConfig, namespace, defaultNamespace string) error {
	simNamespace := fmt.Sprintf("%s%05d", namespace, i)
	simDefaultNamespace := fmt.Sprintf("%s%05d", defaultNamespace, i)

	clusterID, err := setupNamespace(ctx, kubeConfig, namespace, simNamespace)
	if err != nil {
		return err
	}

	return agent.Start(ctx, kubeConfig, simNamespace, &agent.Options{
		DefaultNamespace: simDefaultNamespace,
		ClusterID:        clusterID,
		NoLeaderElect:    true,
	})
}

func setupNamespace(ctx context.Context, kubeConfig, namespace, simNamespace string) (string, error) {
	cfg, err := kubeconfig.GetNonInteractiveClientConfig(kubeConfig).ClientConfig()
	if err != nil {
		return "", err
	}
	cfg.RateLimiter = ratelimit.None

	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", err
	}

	kubeSystem, err := k8s.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	clusterID := name.SafeConcatName(simNamespace, strings.SplitN(string(kubeSystem.UID), "-", 2)[0])

	secret, err := k8s.CoreV1().Secrets(namespace).Get(ctx, register.CredName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	if secret.Annotations[fleet.BootstrapToken] != "true" {
		return "", fmt.Errorf("%s/%s does not have the label %s=true", namespace, register.CredName, fleet.BootstrapToken)
	}

	conf, err := k8s.CoreV1().ConfigMaps(namespace).Get(ctx, config.AgentConfigName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		conf = nil
	} else if err != nil {
		return "", err
	}

	_, err = k8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: simNamespace,
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}

	_, err = k8s.CoreV1().Secrets(simNamespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secret.Name,
			Namespace: simNamespace,
			Annotations: map[string]string{
				fleet.BootstrapToken: "true",
			},
		},
		Data: secret.Data,
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}

	if conf != nil {
		_, err = k8s.CoreV1().ConfigMaps(simNamespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        conf.Name,
				Namespace:   simNamespace,
				Labels:      conf.Labels,
				Annotations: conf.Annotations,
			},
			Data: conf.Data,
		}, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return "", err
		}
	}

	return clusterID, agent.Register(ctx, kubeConfig, simNamespace, clusterID)
}

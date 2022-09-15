// Package simulator simulates multiple agents on the same cluster. (fleetagent)
package simulator

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/rancher/fleet/modules/agent/pkg/agent"
	"github.com/rancher/fleet/modules/agent/pkg/register"
	"github.com/rancher/fleet/pkg/config"

	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/ratelimit"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var (
	semMax = int64(50)
	sem    = semaphore.NewWeighted(semMax)
)

func Simulate(ctx context.Context, count int, kubeConfig, namespace, defaultNamespace string, opts agent.Options) error {
	logrus.Infof("Starting %d simulators", count)

	startAfter := make(chan struct{})
	opts.StartAfter = startAfter

	eg, ctx := errgroup.WithContext(ctx)
	for i := 0; i < count; i++ {
		i := i
		if err := sem.Acquire(ctx, 1); err != nil {
			close(startAfter)
			return err
		}
		logrus.Infof("STARING %s%05d", namespace, i)
		eg.Go(func() error {
			defer sem.Release(1)
			if err := simulateAgent(ctx, i, kubeConfig, namespace, defaultNamespace, opts); err != nil {
				logrus.Errorf("Failed to start simulator %s: %v", namespace, err)
				return err
			}
			return nil
		})
	}

	if err := sem.Acquire(ctx, semMax); err != nil {
		close(startAfter)
		return err
	}
	close(startAfter)

	eg.Go(func() error {
		// wait forever unless one of the simulators dies
		<-ctx.Done()
		return ctx.Err()
	})

	return eg.Wait()
}

func simulateAgent(ctx context.Context, i int, kubeConfig, namespace, defaultNamespace string, opts agent.Options) error {
	simNamespace := fmt.Sprintf("%s%05d", namespace, i)
	simDefaultNamespace := fmt.Sprintf("%s%05d", defaultNamespace, i)

	clusterID, err := setupNamespace(ctx, kubeConfig, namespace, simNamespace)
	if err != nil {
		return err
	}

	opts.DefaultNamespace = simDefaultNamespace
	opts.ClusterID = clusterID
	opts.NoLeaderElect = true
	return agent.Start(ctx, kubeConfig, simNamespace, simNamespace, &opts)
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

	if _, err = k8s.CoreV1().Secrets(simNamespace).Get(ctx, register.CredName, metav1.GetOptions{}); err != nil {
		secret, err := k8s.CoreV1().Secrets(namespace).Get(ctx, register.BootstrapCredName, metav1.GetOptions{})
		if err != nil {
			return "", err
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
			},
			Data: secret.Data,
		}, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return "", err
		}

		if conf != nil {
			conf, err := injectConfig(conf, simNamespace)
			if err != nil {
				return "", err
			}
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
	}

	return clusterID, agent.Register(ctx, kubeConfig, simNamespace, clusterID)
}

func injectConfig(cm *corev1.ConfigMap, simNamespace string) (*corev1.ConfigMap, error) {
	cfg, err := config.ReadConfig(cm)
	if err != nil {
		return nil, err
	}
	if cfg.Labels == nil {
		cfg.Labels = map[string]string{}
	}
	cfg.Labels["fleet.cattle.io/non-managed-agent"] = "true"
	cfg.Labels["simulator-namespace"] = simNamespace
	return config.ToConfigMap(cm.Namespace, cm.Name, cfg)
}

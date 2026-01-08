// Package secret gets or creates service account secrets for cluster registration.
package secret

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/pkg/durations"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetServiceAccountTokenSecret gets or creates a secret for the service
// account. It waits 2 seconds for the data to be populated with a token.
func GetServiceAccountTokenSecret(ctx context.Context, sa *corev1.ServiceAccount, c client.Client) (*corev1.Secret, error) {
	name := sa.Name + "-token"
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Namespace: sa.Namespace, Name: name}, secret)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting secret: %w", err)
		}
		return createServiceAccountTokenSecret(ctx, sa, c)
	}
	return secret, nil
}

func createServiceAccountTokenSecret(ctx context.Context, sa *corev1.ServiceAccount, c client.Client) (*corev1.Secret, error) {
	// create the secret for the serviceAccount
	logrus.Debugf("creating ServiceAccountTokenSecret for sa %v", sa.Name)
	name := sa.Name + "-token"
	sc := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: sa.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "ServiceAccount",
					Name:       sa.Name,
					UID:        sa.UID,
				},
			},
			Annotations: map[string]string{
				"kubernetes.io/service-account.name": sa.Name,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	err := c.Create(ctx, sc)
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("error creating secret: %w", err)
		}
		err = c.Get(ctx, types.NamespacedName{Namespace: sa.Namespace, Name: name}, sc)
		if err != nil {
			logrus.Debugf("secret %v already exists, error getting it", name)
			return nil, fmt.Errorf("error getting secret: %w", err)
		}
	}
	// Kubernetes auto populates the secret token after it is created, for which we should wait
	logrus.Infof("Waiting for service account token key to be populated for secret %s/%s", sc.Namespace, sc.Name)
	if _, ok := sc.Data[corev1.ServiceAccountTokenKey]; !ok {
		for {
			logrus.Debugf("wait for svc account secret to be populated with token %s", sc.Name)
			time.Sleep(durations.ServiceTokenSleep)
			err = c.Get(ctx, types.NamespacedName{Namespace: sa.Namespace, Name: name}, sc)
			if err != nil {
				return nil, err
			}
			if _, ok := sc.Data[corev1.ServiceAccountTokenKey]; ok {
				break
			}
		}
	}
	return sc, nil
}

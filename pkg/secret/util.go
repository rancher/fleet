// Package secret gets or creates service account secrets for cluster registration. (fleetcontroller)
package secret

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetServiceAccountTokenSecret gets or creates a secret for the service
// account. It waits 2 seconds for the data to be populated with a token.
func GetServiceAccountTokenSecret(sa *corev1.ServiceAccount, secretsController corecontrollers.SecretController) (*corev1.Secret, error) {
	name := sa.Name + "-token"
	secret, err := secretsController.Get(sa.Namespace, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting secret: %w", err)
		}
		return createServiceAccountTokenSecret(sa, secretsController)
	}
	return secret, nil
}

func createServiceAccountTokenSecret(sa *corev1.ServiceAccount, secretsController corecontrollers.SecretController) (*corev1.Secret, error) {
	// create the secret for the serviceAccount
	logrus.Debugf("creating ServiceAccountTokenSecret for sa %v", sa.Name)
	name := sa.Name + "-token"
	sc := corev1.Secret{
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
	secret, err := secretsController.Create(&sc)
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("error creating secret: %w", err)
		}
		secret, err = secretsController.Get(sa.Namespace, name, metav1.GetOptions{})
		if err != nil {
			logrus.Debugf("secret %v already exists, error getting it", name)
			return nil, fmt.Errorf("error getting secret: %w", err)
		}
	}
	//Kubernetes auto populates the secret token after it is created, for which we should wait
	logrus.Infof("waiting for service account token key to be populated for secret %s/%s", secret.Namespace, secret.Name)
	if _, ok := secret.Data[corev1.ServiceAccountTokenKey]; !ok {
		for {
			logrus.Debugf("wait for svc account secret to be populated with token %s", secret.Name)
			time.Sleep(2 * time.Second)
			secret, err = secretsController.Get(sa.Namespace, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			if _, ok := secret.Data[corev1.ServiceAccountTokenKey]; ok {
				break
			}
		}
	}
	return secret, nil
}

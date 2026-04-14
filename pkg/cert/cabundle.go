package cert

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const rancherNS = "cattle-system"

func GetRancherCABundle(ctx context.Context, c client.Reader) ([]byte, error) {
	secret := &corev1.Secret{}

	err := c.Get(ctx, types.NamespacedName{Namespace: rancherNS, Name: "tls-ca"}, secret)
	if client.IgnoreNotFound(err) != nil {
		return nil, err
	}

	caBundle, ok := secret.Data["cacerts.pem"]
	if !apierrors.IsNotFound(err) && !ok {
		return nil, errors.New("no field cacerts.pem found in secret tls-ca")
	}

	err = c.Get(ctx, types.NamespacedName{Namespace: rancherNS, Name: "tls-ca-additional"}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return caBundle, nil
		}

		return nil, err
	}

	field, ok := secret.Data["ca-additional.pem"]
	if !ok {
		return nil, errors.New("no field ca-additional.pem found in secret tls-ca-additional")
	}
	caBundle = append(caBundle, field...)

	return caBundle, nil
}

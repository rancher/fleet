package cert

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const rancherNS = "cattle-system"

func GetRancherCABundle(ctx context.Context, c client.Client) ([]byte, error) {
	secret := &corev1.Secret{}

	err := c.Get(ctx, types.NamespacedName{Namespace: rancherNS, Name: "tls-ca"}, secret)
	if client.IgnoreNotFound(err) != nil {
		return nil, err
	}

	caBundle, ok := secret.Data["cacerts.pem"] // TODO check that the path is right, with an actual Rancher install
	if !errors.IsNotFound(err) && !ok {
		return nil, fmt.Errorf("no field cacerts.pem found in secret tls-ca")
	}

	err = c.Get(ctx, types.NamespacedName{Namespace: rancherNS, Name: "tls-ca-additional"}, secret)
	if err != nil {
		if errors.IsNotFound(err) {
			return caBundle, nil
		}

		return nil, err
	}

	field, ok := secret.Data["ca-additional.pem"] // TODO check that the path is right, with an actual Rancher install
	if !ok {
		return nil, fmt.Errorf("no field ca-additional.pem found in secret tls-ca-additional")
	}
	caBundle = append(caBundle, field...)

	return caBundle, nil
}

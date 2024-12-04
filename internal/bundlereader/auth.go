package bundlereader

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Auth struct {
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	CABundle           []byte `json:"caBundle,omitempty"`
	SSHPrivateKey      []byte `json:"sshPrivateKey,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
}

func ReadHelmAuthFromSecret(ctx context.Context, c client.Client, req types.NamespacedName) (Auth, error) {
	if req.Name == "" {
		return Auth{}, nil
	}
	secret := &corev1.Secret{}
	err := c.Get(ctx, req, secret)
	if err != nil {
		return Auth{}, err
	}

	auth := Auth{}
	username, ok := secret.Data[corev1.BasicAuthUsernameKey]
	if ok {
		auth.Username = string(username)
	}

	password, ok := secret.Data[corev1.BasicAuthPasswordKey]
	if ok {
		auth.Password = string(password)
	}

	caBundle, ok := secret.Data["cacerts"]
	if ok {
		auth.CABundle = caBundle
	}

	return auth, nil
}

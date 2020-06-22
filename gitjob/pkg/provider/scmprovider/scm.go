package scmprovider

import (
	webhookv1 "github.com/rancher/gitwatcher/pkg/apis/gitwatcher.cattle.io/v1"
	corev1controller "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

type SCM struct {
	SecretsCache corev1controller.SecretCache
}

func (s *SCM) GetSecret(nsSecret string, obj *webhookv1.GitWatcher) (*v1.Secret, error) {
	secret, err := s.SecretsCache.Get(obj.Namespace, obj.Spec.RepositoryCredentialSecretName)
	if errors.IsNotFound(err) {
		secret = nil
	} else if err != nil {
		return nil, err
	}

	if secret != nil {
		return secret, nil
	}

	return s.SecretsCache.Get(obj.Namespace, nsSecret)
}

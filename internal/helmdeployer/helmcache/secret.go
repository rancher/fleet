package helmcache

import (
	"context"

	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
)

// SecretClient implements methods to handle secrets. Get and list will be retrieved from the wrangler cache, the other calls
// will be make to the Kubernetes API server.
type SecretClient struct {
	cache     corecontrollers.SecretCache
	client    kubernetes.Interface
	namespace string
}

func NewSecretClient(cache corecontrollers.SecretCache, client kubernetes.Interface, namespace string) *SecretClient {
	return &SecretClient{cache, client, namespace}
}

// Create creates a secret using a k8s client that calls the Kubernetes API server
func (s *SecretClient) Create(ctx context.Context, secret *v1.Secret, opts metav1.CreateOptions) (*v1.Secret, error) {
	return s.client.CoreV1().Secrets(s.namespace).Create(ctx, secret, opts)
}

// Update updates a secret using a k8s client that calls the Kubernetes API server
func (s *SecretClient) Update(ctx context.Context, secret *v1.Secret, opts metav1.UpdateOptions) (*v1.Secret, error) {
	return s.client.CoreV1().Secrets(s.namespace).Update(ctx, secret, opts)
}

// Delete deletes a secret using a k8s client that calls the Kubernetes API server
func (s *SecretClient) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	return s.client.CoreV1().Secrets(s.namespace).Delete(ctx, name, opts)
}

// DeleteCollection deletes a secret collection using a k8s client that calls the Kubernetes API server
func (s *SecretClient) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return s.client.CoreV1().Secrets(s.namespace).DeleteCollection(ctx, opts, listOpts)
}

// Get gets a secret from the wrangler cache.
func (s *SecretClient) Get(ctx context.Context, name string, opts metav1.GetOptions) (*v1.Secret, error) {
	return s.cache.Get(s.namespace, name)
}

// List lists secrets from the wrangler cache.
func (s *SecretClient) List(ctx context.Context, opts metav1.ListOptions) (*v1.SecretList, error) {
	labels, err := labels.Parse(opts.LabelSelector)
	if err != nil {
		return nil, err
	}
	secrets, err := s.cache.List(s.namespace, labels)
	if err != nil {
		return nil, err
	}

	var items []v1.Secret
	for _, secret := range secrets {
		items = append(items, *secret)
	}

	return &v1.SecretList{
		Items: items,
	}, nil
}

// Watch watches a secret using a k8s client that calls the Kubernetes API server
func (s *SecretClient) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return s.client.CoreV1().Secrets(s.namespace).Watch(ctx, opts)
}

// Patch patches a secret using a k8s client that calls the Kubernetes API server
func (s *SecretClient) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*v1.Secret, error) {
	return s.client.CoreV1().Secrets(s.namespace).Patch(ctx, name, pt, data, opts, subresources...)
}

// Apply applies a secret using a k8s client that calls the Kubernetes API server
func (s *SecretClient) Apply(ctx context.Context, secretConfiguration *applycorev1.SecretApplyConfiguration, opts metav1.ApplyOptions) (*v1.Secret, error) {
	return s.client.CoreV1().Secrets(s.namespace).Apply(ctx, secretConfiguration, opts)
}

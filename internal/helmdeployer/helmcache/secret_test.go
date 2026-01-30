package helmcache

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	secretName      = "test"
	secretNamespace = "test-ns"
)

var defaultSecret = corev1.Secret{
	ObjectMeta: metav1.ObjectMeta{
		Name:      secretName,
		Namespace: secretNamespace,
	},
}

// ptr returns a pointer to the given string
func ptr(s string) *string {
	return &s
}

func TestGet(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	secret := defaultSecret
	cache := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&secret).Build()
	secretClient := NewSecretClient(cache, nil, secretNamespace)

	secretGot, err := secretClient.Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if secretGot.Name != secret.Name || secretGot.Namespace != secret.Namespace {
		t.Errorf("expected secret name %s namespace %s, got name %s namespace %s",
			secret.Name, secret.Namespace, secretGot.Name, secretGot.Namespace)
	}
	if !reflect.DeepEqual(secret.Data, secretGot.Data) {
		t.Errorf("expected secret data %#v, got %#v", secret.Data, secretGot.Data)
	}
}

func TestList(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	secret := defaultSecret
	secret.Labels = map[string]string{"foo": "bar"}
	cache := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&secret).Build()
	secretClient := NewSecretClient(cache, nil, secretNamespace)

	secretList, err := secretClient.List(context.TODO(), metav1.ListOptions{LabelSelector: "foo=bar"})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if len(secretList.Items) != 1 {
		t.Errorf("expected secret list to have 1 element, got %v", len(secretList.Items))
	}
	item := secretList.Items[0]
	if item.Name != secret.Name || item.Namespace != secret.Namespace {
		t.Errorf("expected secret name %s namespace %s, got name %s namespace %s",
			secret.Name, secret.Namespace, item.Name, item.Namespace)
	}
	if !reflect.DeepEqual(secret.Labels, item.Labels) {
		t.Errorf("expected labels %v, got %v", secret.Labels, item.Labels)
	}
	if !reflect.DeepEqual(secret.Data, item.Data) {
		t.Errorf("expected secret data %#v, got %#v", secret.Data, item.Data)
	}
}

func TestCreate(t *testing.T) {
	client := k8sfake.NewClientset()
	secretClient := NewSecretClient(nil, client, secretNamespace)
	secret := defaultSecret
	secretCreated, err := secretClient.Create(context.TODO(), &secret, metav1.CreateOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if secretCreated.Name != secret.Name || secretCreated.Namespace != secret.Namespace {
		t.Errorf("expected secret %v, got %v", secret, secretCreated)
	}
}

func TestUpdate(t *testing.T) {
	secretUpdate := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Data: map[string][]byte{"test": []byte("data")},
	}
	secret := defaultSecret
	client := k8sfake.NewClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	secretUpdated, err := secretClient.Update(context.TODO(), &secretUpdate, metav1.UpdateOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if secretUpdated.Name != secretUpdate.Name || secretUpdated.Namespace != secretUpdate.Namespace {
		t.Errorf("expected secret name %s namespace %s, got name %s namespace %s",
			secretUpdate.Name, secretUpdate.Namespace, secretUpdated.Name, secretUpdated.Namespace)
	}
	if !reflect.DeepEqual(secretUpdate.Data, secretUpdated.Data) {
		t.Errorf("expected data %v, got %v", secretUpdate.Data, secretUpdated.Data)
	}
}

func TestDelete(t *testing.T) {
	secret := defaultSecret
	client := k8sfake.NewClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	err := secretClient.Delete(context.TODO(), secretName, metav1.DeleteOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestDeleteCollection(t *testing.T) {
	secret := defaultSecret
	client := k8sfake.NewClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	err := secretClient.DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{FieldSelector: "name=" + secretName})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestWatch(t *testing.T) {
	secret := defaultSecret
	client := k8sfake.NewClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	watch, err := secretClient.Watch(context.TODO(), metav1.ListOptions{FieldSelector: "name=" + secretName})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if watch == nil {
		t.Errorf("watch should not be nil")
	}
}

func TestPatch(t *testing.T) {
	secret := defaultSecret
	client := k8sfake.NewClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	patch := []byte(`{"data":{"test":"Y29udGVudA=="}}`) // "content", base64-encoded
	secretPatched, err := secretClient.Patch(context.TODO(), secretName, types.StrategicMergePatchType, patch, metav1.PatchOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if secretPatched.Name != secretName || secretPatched.Namespace != secretNamespace {
		t.Errorf("expected secret name %s namespace %s, got name %s namespace %s",
			secretName, secretNamespace, secretPatched.Name, secretPatched.Namespace)
	}
	expectedData := []byte("content")
	if !reflect.DeepEqual(expectedData, secretPatched.Data["test"]) {
		t.Errorf("expected data 'content', got %s", string(secretPatched.Data["test"]))
	}
}

func TestApply(t *testing.T) {
	secret := defaultSecret
	client := k8sfake.NewClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	secretName := "test"
	namespace := secretNamespace
	secretApplied, err := secretClient.Apply(context.TODO(), &applycorev1.SecretApplyConfiguration{
		TypeMetaApplyConfiguration: v1.TypeMetaApplyConfiguration{
			APIVersion: ptr("v1"),
			Kind:       ptr("Secret"),
		},
		ObjectMetaApplyConfiguration: &v1.ObjectMetaApplyConfiguration{
			Name:      &secretName,
			Namespace: &namespace,
		},
		Data: map[string][]byte{"test": []byte("content")},
	}, metav1.ApplyOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	// Check the key fields instead of deep equality
	if secretApplied.Name != secretName || secretApplied.Namespace != namespace {
		t.Errorf("expected secret name %s namespace %s, got name %s namespace %s",
			secretName, namespace, secretApplied.Name, secretApplied.Namespace)
	}
	if string(secretApplied.Data["test"]) != "content" {
		t.Errorf("expected data 'content', got %s", string(secretApplied.Data["test"]))
	}
}

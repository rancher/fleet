package helmcache

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

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
	if !cmp.Equal(secret.ObjectMeta, secretGot.ObjectMeta) {
		t.Errorf("expected secret meta %#v, got %#v", secret.ObjectMeta, secretGot.ObjectMeta)
	}
	if !cmp.Equal(secret.Data, secretGot.Data) {
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
	if !cmp.Equal(secret.ObjectMeta, secretList.Items[0].ObjectMeta) {
		t.Errorf("expected secret meta %#v, got %#v", secret, secretList.Items[0])
	}
	if !cmp.Equal(secret.Data, secretList.Items[0].Data) {
		t.Errorf("expected secret data %#v, got %#v", secret, secretList.Items[0])
	}
}

func TestCreate(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	secretClient := NewSecretClient(nil, client, secretNamespace)
	secret := defaultSecret
	secretCreated, err := secretClient.Create(context.TODO(), &secret, metav1.CreateOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if !cmp.Equal(&secret, secretCreated) {
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
	client := k8sfake.NewSimpleClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	secretUpdated, err := secretClient.Update(context.TODO(), &secretUpdate, metav1.UpdateOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if !cmp.Equal(&secretUpdate, secretUpdated) {
		t.Errorf("expected secret %v, got %v", secretUpdate, secretUpdated)
	}
}

func TestDelete(t *testing.T) {
	secret := defaultSecret
	client := k8sfake.NewSimpleClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	err := secretClient.Delete(context.TODO(), secretName, metav1.DeleteOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestDeleteCollection(t *testing.T) {
	secret := defaultSecret
	client := k8sfake.NewSimpleClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	err := secretClient.DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{FieldSelector: "name=" + secretName})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestWatch(t *testing.T) {
	secret := defaultSecret
	client := k8sfake.NewSimpleClientset(&secret)
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
	secretPatch := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Data: map[string][]byte{"test": []byte("content")},
	}
	secret := defaultSecret
	client := k8sfake.NewSimpleClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	patch := []byte(`{"data":{"test":"Y29udGVudA=="}}`) // "content", base64-encoded
	secretPatched, err := secretClient.Patch(context.TODO(), secretName, types.StrategicMergePatchType, patch, metav1.PatchOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if !cmp.Equal(&secretPatch, secretPatched) {
		t.Errorf("expected secret %v, got %v", secretPatch, secretPatched)
	}
}

func TestApply(t *testing.T) {
	secretApply := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Data: map[string][]byte{"test": []byte("content")},
	}
	secret := defaultSecret
	client := k8sfake.NewSimpleClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	secretName := "test"
	secretApplied, err := secretClient.Apply(context.TODO(), &applycorev1.SecretApplyConfiguration{
		ObjectMetaApplyConfiguration: &v1.ObjectMetaApplyConfiguration{
			Name: &secretName,
		},
		Data: map[string][]byte{"test": []byte("content")},
	}, metav1.ApplyOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if !cmp.Equal(&secretApply, secretApplied) {
		t.Errorf("expected secret %v, got %v", secretApply, secretApplied)
	}
}

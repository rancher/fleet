package helmcache

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/rancher/wrangler/v2/pkg/generic/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

const (
	secretName      = "test"
	secretNamespace = "test-ns"
)

var secret = corev1.Secret{
	ObjectMeta: metav1.ObjectMeta{
		Name:      secretName,
		Namespace: secretNamespace,
	},
}

func TestGet(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
	secretClient := NewSecretClient(mockCache, nil, secretNamespace)
	mockCache.EXPECT().Get(secretNamespace, secretName).Return(&secret, nil)

	secretGot, err := secretClient.Get(context.TODO(), secretName, metav1.GetOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if !cmp.Equal(&secret, secretGot) {
		t.Errorf("expected secret %v, got %v", secret, secretGot)
	}
}

func TestList(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
	secretClient := NewSecretClient(mockCache, nil, secretNamespace)
	labelSelector, err := labels.Parse("foo=bar")
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	mockCache.EXPECT().List(secretNamespace, labelSelector).Return([]*corev1.Secret{&secret}, nil)
	secretList, err := secretClient.List(context.TODO(), metav1.ListOptions{LabelSelector: "foo=bar"})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	secretListExpected := &corev1.SecretList{Items: []corev1.Secret{secret}}
	if !cmp.Equal(secretListExpected, secretList) {
		t.Errorf("expected secret %v, got %v", secretListExpected, secretList)
	}
}

func TestCreate(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	secretClient := NewSecretClient(nil, client, secretNamespace)
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
	client := k8sfake.NewSimpleClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	err := secretClient.Delete(context.TODO(), secretName, metav1.DeleteOptions{})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestDeleteCollection(t *testing.T) {
	client := k8sfake.NewSimpleClientset(&secret)
	secretClient := NewSecretClient(nil, client, secretNamespace)
	err := secretClient.DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{FieldSelector: "name=" + secretName})

	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestWatch(t *testing.T) {
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

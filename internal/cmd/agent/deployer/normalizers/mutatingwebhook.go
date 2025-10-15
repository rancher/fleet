package normalizers

import (
	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"

	adregv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type MutatingWebhookNormalizer struct {
	Live objectset.ObjectByGVK
}

func (m *MutatingWebhookNormalizer) Normalize(un *unstructured.Unstructured) error {
	if un == nil {
		return nil
	}
	gvk := un.GroupVersionKind()
	if gvk.Group != adregv1.GroupName || gvk.Kind != "MutatingWebhookConfiguration" {
		return nil
	}

	return m.convertMutatingWebhookV1(un)
}

func (m *MutatingWebhookNormalizer) convertMutatingWebhookV1(un *unstructured.Unstructured) error {
	var webhook adregv1.MutatingWebhookConfiguration
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(un.Object, &webhook)
	if err != nil {
		logrus.Errorf("Failed to convert unstructured to webhook, err: %v", err)
		return nil
	}

	for i, config := range webhook.Webhooks {
		if webhook.UID == "" && string(config.ClientConfig.CABundle) == "\n" {
			live := lookupLive(un.GroupVersionKind(), un.GetName(), un.GetNamespace(), m.Live)
			liveWebhook, ok := live.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			if err := setMutatingWebhookV1CacertNil(liveWebhook, i); err != nil {
				logrus.Errorf("Failed to normalize webhook cacert, err: %v", err)
				return nil
			}
		}
	}
	return nil
}

func setMutatingWebhookV1CacertNil(un *unstructured.Unstructured, index int) error {
	var webhook adregv1.MutatingWebhookConfiguration
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(un.Object, &webhook)
	if err != nil {
		logrus.Errorf("Failed to convert unstructured to webhook, err: %v", err)
		return err
	}

	if index >= len(webhook.Webhooks) {
		return nil
	}
	webhook.Webhooks[index].ClientConfig.CABundle = nil
	newObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&webhook)
	if err != nil {
		logrus.Errorf("Failed to convert unstructured to webhook, err: %v", err)
		return err
	}
	if webhook.Webhooks != nil {
		if err = unstructured.SetNestedField(un.Object, newObj["webhooks"], "webhooks"); err != nil {
			logrus.Errorf("MutatingWebhook normalization error: %v", err)
			return err
		}
	}
	return nil
}

func lookupLive(gvk schema.GroupVersionKind, name, namespace string, live objectset.ObjectByGVK) runtime.Object {
	key := objectset.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	return live[gvk][key]
}

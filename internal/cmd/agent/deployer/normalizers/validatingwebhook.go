package normalizers

import (
	"github.com/sirupsen/logrus"

	"github.com/rancher/wrangler/v3/pkg/objectset"
	adregv1 "k8s.io/api/admissionregistration/v1"
	adregv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type ValidatingWebhookNormalizer struct {
	Live objectset.ObjectByGVK
}

func (v *ValidatingWebhookNormalizer) Normalize(un *unstructured.Unstructured) error {
	if un == nil {
		return nil
	}
	gvk := un.GroupVersionKind()
	if gvk.Group != adregv1.GroupName || gvk.Kind != "ValidatingWebhookConfiguration" {
		return nil
	}

	if gvk.Version == "v1beta1" {
		return v.convertValidatingWebhookV1beta1(un)
	}

	return v.convertValidatingWebhookV1(un)
}

func (v *ValidatingWebhookNormalizer) convertValidatingWebhookV1beta1(un *unstructured.Unstructured) error {
	var webhook adregv1beta1.ValidatingWebhookConfiguration
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(un.Object, &webhook)
	if err != nil {
		logrus.Error("Failed to convert unstructured to webhook")
		return nil
	}

	for i, config := range webhook.Webhooks {
		if webhook.UID == "" && string(config.ClientConfig.CABundle) == "\n" {
			live := lookupLive(un.GroupVersionKind(), un.GetName(), un.GetNamespace(), v.Live)
			liveWebhook, ok := live.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			if err := setValidatingWebhookV1beta1CacertNil(liveWebhook, i); err != nil {
				logrus.Errorf("Failed to normalize webhook cacert, err: %v", err)
				return nil
			}
		}
	}
	return nil
}

func (v *ValidatingWebhookNormalizer) convertValidatingWebhookV1(un *unstructured.Unstructured) error {
	var webhook adregv1.ValidatingWebhookConfiguration
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(un.Object, &webhook)
	if err != nil {
		logrus.Errorf("Failed to convert unstructured to webhook, err: %v", err)
		return nil
	}

	for i, config := range webhook.Webhooks {
		if webhook.UID == "" && string(config.ClientConfig.CABundle) == "\n" {
			live := lookupLive(un.GroupVersionKind(), un.GetName(), un.GetNamespace(), v.Live)
			liveWebhook, ok := live.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			if err := setValidatingWebhookV1CacertNil(liveWebhook, i); err != nil {
				logrus.Errorf("Failed to normalize webhook cacert, err: %v", err)
				return nil
			}
		}
	}
	return nil
}

func setValidatingWebhookV1CacertNil(un *unstructured.Unstructured, index int) error {
	var webhook adregv1.ValidatingWebhookConfiguration
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
			logrus.Errorf("ValidatingWebhook normalization error: %v", err)
			return err
		}
	}
	return nil
}

func setValidatingWebhookV1beta1CacertNil(un *unstructured.Unstructured, index int) error {
	var webhook adregv1beta1.ValidatingWebhookConfiguration
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(un.Object, &webhook)
	if err != nil {
		logrus.Error("Failed to convert unstructured to webhook")
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
			logrus.Errorf("ValidatingWebhook normalization error: %v", err)
			return err
		}
	}
	return nil
}

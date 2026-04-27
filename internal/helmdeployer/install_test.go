package helmdeployer

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
)

func TestValuesFrom(t *testing.T) {
	a := assert.New(t)
	key := "values.yaml"
	newline := "\n"
	if runtime.GOOS == "windows" {
		newline = "\r\n"
	}

	configMapPayload := fmt.Sprintf("replication: \"true\"%sreplicas: \"2\"%sserviceType: NodePort", newline, newline)
	secretPayload := fmt.Sprintf("replication: \"false\"%sreplicas: \"3\"%sserviceType: NodePort%sfoo: bar", newline, newline, newline)
	totalValues := map[string]interface{}{"beforeMerge": "value"}
	expected := map[string]interface{}{
		"beforeMerge": "value",
		"replicas":    "2",
		"replication": "true",
		"serviceType": "NodePort",
		"foo":         "bar",
	}

	configMapName := "configmap-name"
	configMapNamespace := "configmap-namespace"
	configMapValues, err := valuesFromConfigMap(configMapName, configMapNamespace, key, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: configMapNamespace,
		},
		Data: map[string]string{
			key: configMapPayload,
		},
	})
	a.NoError(err)

	secretName := "secret-name"
	secretNamespace := "secret-namespace"
	secretValues, err := valuesFromSecret(secretName, secretNamespace, key, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Data: map[string][]byte{
			key: []byte(secretPayload),
		},
	})
	a.NoError(err)

	totalValues = mergeValues(totalValues, secretValues)
	totalValues = mergeValues(totalValues, configMapValues)
	a.Equal(expected, totalValues)
}

func TestValuesFromUsesExplicitNamespace(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	defaultNS := "helm-default"
	explicitNS := "explicit-ns"

	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm-explicit", Namespace: explicitNS},
		Data:       map[string]string{DefaultKey: "cmVal: explicit"},
	}

	kubeClient := kubernetesfake.NewSimpleClientset(&cm)
	h := &Helm{template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
				LocalObjectReference: fleet.LocalObjectReference{Name: "cm-explicit"},
				Namespace:            explicitNS,
			}}},
		},
	}

	vals, err := h.getValues(context.TODO(), opts, defaultNS, kubeClient)
	r.NoError(err)
	a.Equal("explicit", vals["cmVal"])
}

func TestValuesFromUsesDefaultNamespaceWhenNoneSpecified(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	defaultNS := "helm-default"

	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm-default", Namespace: defaultNS},
		Data:       map[string]string{DefaultKey: "cmVal: fromDefault"},
	}
	sec := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sec-default", Namespace: defaultNS},
		Data:       map[string][]byte{DefaultKey: []byte("secVal: fromDefault")},
	}

	kubeClient := kubernetesfake.NewSimpleClientset(&cm, &sec)
	h := &Helm{template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-default"}}},
				{SecretKeyRef: &fleet.SecretKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "sec-default"}}},
			},
		},
	}

	vals, err := h.getValues(context.TODO(), opts, defaultNS, kubeClient)
	r.NoError(err)
	a.Equal("fromDefault", vals["cmVal"])
	a.Equal("fromDefault", vals["secVal"])
}

func TestValuesFromReturnsNotFoundWhenResourceMissing(t *testing.T) {
	r := require.New(t)

	kubeClient := kubernetesfake.NewSimpleClientset()
	h := &Helm{template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
					LocalObjectReference: fleet.LocalObjectReference{Name: "missing"},
					Namespace:            "some-ns",
				}},
			},
		},
	}

	_, err := h.getValues(context.TODO(), opts, "default-ns", kubeClient)
	r.Error(err)
	r.True(apierrors.IsNotFound(err), "expected a NotFound error when valuesFrom references a missing resource")
}

func TestValuesFromSkipsLookupInTemplateMode(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	// nil kubeClient signals template mode — no lookups should be performed
	h := &Helm{template: true}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			Values: &fleet.GenericMap{Data: map[string]interface{}{"static": "value"}},
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
					LocalObjectReference: fleet.LocalObjectReference{Name: "should-not-be-read"},
					Namespace:            "some-ns",
				}},
			},
		},
	}

	vals, err := h.getValues(context.TODO(), opts, "default", nil)
	r.NoError(err)
	a.Equal("value", vals["static"])
	_, hasUnwanted := vals["should-not-be-read"]
	a.False(hasUnwanted, "template mode should not read from cluster")
}

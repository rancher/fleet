package helmdeployer

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"os"

	"github.com/rancher/fleet/internal/experimental"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestIsInDownstreamResources(t *testing.T) {
	a := assert.New(t)

	opts := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "ConfigMap", Name: "my-config"},
			{Kind: "Secret", Name: "some-secret"},
		},
	}

	// function returns only a boolean indicating membership for a kind
	// enable experimental feature for this test
	os.Setenv(experimental.CopyResourcesDownstreamFlag, "true")
	defer os.Unsetenv(experimental.CopyResourcesDownstreamFlag)

	found := isInDownstreamResources("my-config", "ConfigMap", opts)
	a.True(found, "expected to find my-config in DownstreamResources")

	found2 := isInDownstreamResources("not-present", "ConfigMap", opts)
	a.False(found2, "expected not to find not-present in DownstreamResources")

	found3 := isInDownstreamResources("my-config", "SomeOtherKind", opts)
	a.False(found3, "expected not to find my-config of kind SomeOtherKind in DownstreamResources")

	found4 := isInDownstreamResources("some-secret", "Secret", opts)
	a.True(found4, "expected to find some-secret in DownstreamResources")

	found5 := isInDownstreamResources("not-present", "Secret", opts)
	a.False(found5, "expected not to find not-present in DownstreamResources")

	// Test case-insensitive kind matching for the parameter
	found6 := isInDownstreamResources("my-config", "configmap", opts)
	a.True(found6, "expected to find my-config with lowercase kind 'configmap'")

	found7 := isInDownstreamResources("my-config", "CONFIGMAP", opts)
	a.True(found7, "expected to find my-config with uppercase kind 'CONFIGMAP'")

	found8 := isInDownstreamResources("some-secret", "secret", opts)
	a.True(found8, "expected to find some-secret with lowercase kind 'secret'")

	found9 := isInDownstreamResources("some-secret", "SECRET", opts)
	a.True(found9, "expected to find some-secret with uppercase kind 'SECRET'")

	found10 := isInDownstreamResources("my-config", "CoNfIgMaP", opts)
	a.True(found10, "expected to find my-config with mixed case kind 'CoNfIgMaP'")

	found11 := isInDownstreamResources("not-present", "configmap", opts)
	a.False(found11, "expected not to find not-present even with lowercase kind")

	// Test case-insensitive kind matching for the DownstreamResources Kind field
	optsLowercaseKind := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "configmap", Name: "my-config-lower"},
			{Kind: "secret", Name: "some-secret-lower"},
		},
	}

	found12 := isInDownstreamResources("my-config-lower", "ConfigMap", optsLowercaseKind)
	a.True(found12, "expected to find my-config-lower when DownstreamResource has lowercase 'configmap'")

	found13 := isInDownstreamResources("my-config-lower", "CONFIGMAP", optsLowercaseKind)
	a.True(found13, "expected to find my-config-lower with uppercase parameter and lowercase DownstreamResource kind")

	found14 := isInDownstreamResources("some-secret-lower", "Secret", optsLowercaseKind)
	a.True(found14, "expected to find some-secret-lower when DownstreamResource has lowercase 'secret'")

	optsUppercaseKind := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "CONFIGMAP", Name: "my-config-upper"},
			{Kind: "SECRET", Name: "some-secret-upper"},
		},
	}

	found15 := isInDownstreamResources("my-config-upper", "ConfigMap", optsUppercaseKind)
	a.True(found15, "expected to find my-config-upper when DownstreamResource has uppercase 'CONFIGMAP'")

	found16 := isInDownstreamResources("my-config-upper", "configmap", optsUppercaseKind)
	a.True(found16, "expected to find my-config-upper with lowercase parameter and uppercase DownstreamResource kind")

	found17 := isInDownstreamResources("some-secret-upper", "secret", optsUppercaseKind)
	a.True(found17, "expected to find some-secret-upper with lowercase parameter and uppercase DownstreamResource kind")

	optsMixedKind := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "CoNfIgMaP", Name: "my-config-mixed"},
		},
	}

	found18 := isInDownstreamResources("my-config-mixed", "ConfigMap", optsMixedKind)
	a.True(found18, "expected to find my-config-mixed when DownstreamResource has mixed case 'CoNfIgMaP'")

	found19 := isInDownstreamResources("my-config-mixed", "configmap", optsMixedKind)
	a.True(found19, "expected to find my-config-mixed with lowercase parameter and mixed case DownstreamResource kind")
}

func TestValuesFromUsesDefaultNamespaceWhenResourceCopiedDownstream(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	scheme := kruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// default namespace where the Helm release lives
	defaultNS := "helm-default"

	// Create a ConfigMap and Secret in the default namespace which should be picked
	// when the valuesFrom reference is part of DownstreamResources.
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm-down", Namespace: defaultNS},
		Data:       map[string]string{DefaultKey: "cmVal: cmDefault"},
	}

	sec := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sec-down", Namespace: defaultNS},
		Data:       map[string][]byte{DefaultKey: []byte("secVal: secDefault")},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&cm, &sec).Build()

	h := &Helm{client: cl, template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-down"}}},
				{SecretKeyRef: &fleet.SecretKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "sec-down"}, Namespace: "ignored-ns"}},
			},
		},
		// copied resources: meaning these exist in the defaultNamespace of the release
		DownstreamResources: []fleet.DownstreamResource{{Kind: "ConfigMap", Name: "cm-down"}, {Kind: "Secret", Name: "sec-down"}},
	}

	// enable experimental copy behavior for this test
	os.Setenv(experimental.CopyResourcesDownstreamFlag, "true")
	defer os.Unsetenv(experimental.CopyResourcesDownstreamFlag)

	vals, err := h.getValues(context.TODO(), opts, defaultNS)
	r.NoError(err)

	// configmap and secret data should have been read from defaultNS
	a.Equal("cmDefault", vals["cmVal"])
	a.Equal("secDefault", vals["secVal"])
}

func TestValuesFromUsesProvidedNamespaceWhenNotCopiedDownstream(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	scheme := kruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// namespaces
	defaultNS := "helm-default"
	providedNS := "explicit-ns"

	// ConfigMap present only in providedNS
	cmProvided := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm-provided", Namespace: providedNS},
		Data:       map[string]string{DefaultKey: "cmVal: cmProvided"},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&cmProvided).Build()
	h := &Helm{client: cl, template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-provided"}, Namespace: providedNS}}},
		},
		// DownstreamResources does NOT list cm-provided — so the provided namespace should be used
		DownstreamResources: []fleet.DownstreamResource{{Kind: "ConfigMap", Name: "some-other"}},
	}

	// enable experimental copy behavior for this test
	os.Setenv(experimental.CopyResourcesDownstreamFlag, "true")
	defer os.Unsetenv(experimental.CopyResourcesDownstreamFlag)

	vals, err := h.getValues(context.TODO(), opts, defaultNS)
	r.NoError(err)
	a.Equal("cmProvided", vals["cmVal"])
}

func TestValuesFromErrorWhenCopiedDownstreamButExperimentalDisabled(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	scheme := kruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := &Helm{client: cl, template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-down"}, Namespace: "provided-ns"}},
				{SecretKeyRef: &fleet.SecretKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "sec-down"}, Namespace: "provided-ns"}},
			},
		},
		DownstreamResources: []fleet.DownstreamResource{{Kind: "ConfigMap", Name: "cm-down"}, {Kind: "Secret", Name: "sec-down"}},
	}

	// ensure experimental feature is disabled
	os.Unsetenv(experimental.CopyResourcesDownstreamFlag)

	_, err := h.getValues(context.TODO(), opts, "default-ns")
	r.Error(err)
	// get will fail trying to read from provided-ns and should report not found
	a.True(apierrors.IsNotFound(err), "expected a NotFound error when valuesFrom references resources and experimental feature is disabled")
}

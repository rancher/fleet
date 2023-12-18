package helmdeployer

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"

	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/yaml"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
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

func TestPostRenderer_Run_DeleteCRDs(t *testing.T) {
	tests := map[string]struct {
		crd                 *apiextensionsv1.CustomResourceDefinition
		opts                v1alpha1.BundleDeploymentOptions
		expectedAnnotations map[string]string
	}{
		"default (no DeleteCRDResources specified)": {
			crd: &apiextensionsv1.CustomResourceDefinition{
				TypeMeta: metav1.TypeMeta{
					Kind:       CRDKind,
					APIVersion: "apiextensions.k8s.io/v1",
				},
			},
			opts: v1alpha1.BundleDeploymentOptions{},
			expectedAnnotations: map[string]string{
				kube.ResourcePolicyAnno:      kube.KeepPolicy,
				"objectset.rio.cattle.io/id": "-",
			},
		},
		"DeleteCRDResources set to true": {
			crd: &apiextensionsv1.CustomResourceDefinition{
				TypeMeta: metav1.TypeMeta{
					Kind:       CRDKind,
					APIVersion: "apiextensions.k8s.io/v1",
				},
			},
			opts: v1alpha1.BundleDeploymentOptions{
				DeleteCRDResources: true,
			},
			expectedAnnotations: map[string]string{
				"objectset.rio.cattle.io/id": "-",
			},
		},
		"DeleteCRDResources set to false": {
			crd: &apiextensionsv1.CustomResourceDefinition{
				TypeMeta: metav1.TypeMeta{
					Kind:       CRDKind,
					APIVersion: "apiextensions.k8s.io/v1",
				},
			},
			opts: v1alpha1.BundleDeploymentOptions{
				DeleteCRDResources: false,
			},
			expectedAnnotations: map[string]string{
				kube.ResourcePolicyAnno:      kube.KeepPolicy,
				"objectset.rio.cattle.io/id": "-",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			data, err := yaml.ToBytes([]kruntime.Object{test.crd})
			if err != nil {
				t.Errorf("unexpected error %v", err)
			}
			renderedManifests := bytes.NewBuffer(data)

			pr := postRender{
				manifest: &manifest.Manifest{
					Resources: []v1alpha1.BundleResource{},
				},
				chart: &chart.Chart{},
				opts:  test.opts,
			}
			postRenderedManifests, err := pr.Run(renderedManifests)
			if err != nil {
				t.Errorf("unexpected error %v", err)
			}

			data = postRenderedManifests.Bytes()
			objs, err := yaml.ToObjects(bytes.NewBuffer(data))
			if err != nil {
				t.Errorf("unexpected error %v", err)
			}

			m, err := meta.Accessor(objs[0])
			if err != nil {
				t.Errorf("unexpected error %v", err)
			}
			if !cmp.Equal(m.GetAnnotations(), test.expectedAnnotations) {
				t.Errorf("expected %s, got %s", test.expectedAnnotations, m.GetAnnotations())
			}
		})
	}
}

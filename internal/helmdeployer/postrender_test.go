package helmdeployer

import (
	"bytes"
	"testing"

	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/yaml"

	"github.com/google/go-cmp/cmp"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
)

func TestPostRenderer_Run_DeleteCRDs(t *testing.T) {
	tests := map[string]struct {
		obj                 kruntime.Object
		opts                v1alpha1.BundleDeploymentOptions
		expectedAnnotations map[string]string
	}{
		"default (no DeleteCRDResources specified)": {
			obj: &apiextensionsv1.CustomResourceDefinition{
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
			obj: &apiextensionsv1.CustomResourceDefinition{
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
			obj: &apiextensionsv1.CustomResourceDefinition{
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
		"Annotation not added for non CRDs resources": {
			obj: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					Kind: "Pod",
				},
			},
			opts: v1alpha1.BundleDeploymentOptions{
				DeleteCRDResources: false,
			},
			expectedAnnotations: map[string]string{
				"objectset.rio.cattle.io/id": "-",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			data, err := yaml.ToBytes([]kruntime.Object{test.obj})
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

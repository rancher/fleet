package helmdeployer

import (
	"bytes"
	"testing"

	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/yaml"

	"github.com/google/go-cmp/cmp"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/kube"
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
				chart: &chartv2.Chart{},
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

	t.Run("Multiple resources, only add to CRDs", func(t *testing.T) {
		crd := &apiextensionsv1.CustomResourceDefinition{
			TypeMeta: metav1.TypeMeta{
				Kind:       CRDKind,
				APIVersion: "apiextensions.k8s.io/v1",
			},
		}
		pod := &corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				Kind: "Pod",
			},
		}

		data, err := yaml.ToBytes([]kruntime.Object{crd, pod})
		if err != nil {
			t.Errorf("unexpected error %v", err)
		}
		renderedManifests := bytes.NewBuffer(data)

		pr := postRender{
			manifest: &manifest.Manifest{
				Resources: []v1alpha1.BundleResource{},
			},
			chart: &chartv2.Chart{},
			opts: v1alpha1.BundleDeploymentOptions{
				DeleteCRDResources: false,
			},
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

		for _, obj := range objs {
			m, err := meta.Accessor(obj)
			if err != nil {
				t.Errorf("unexpected error %v", err)
			}

			annotations := m.GetAnnotations()
			kind := obj.GetObjectKind().GroupVersionKind().Kind
			if kind == CRDKind {
				if val, ok := annotations[kube.ResourcePolicyAnno]; !ok || val != kube.KeepPolicy {
					t.Errorf("expected %s, got %s", kube.KeepPolicy, annotations[kube.ResourcePolicyAnno])
				}
			} else {
				if val, ok := annotations[kube.ResourcePolicyAnno]; ok {
					t.Errorf("unexpected annotation on %s, got %s: %s", kind, kube.ResourcePolicyAnno, val)
				}
			}
		}
	})

}

func TestHasFlowStyleCandidate(t *testing.T) {
	tests := map[string]struct {
		input string
		want  bool
	}{
		"empty": {input: "", want: false},
		"block-style only": {
			input: "apiVersion: v1\nkind: ConfigMap\n",
			want:  false,
		},
		"flow-style, no marker": {
			// Document starting directly with '{' (no leading "---").
			input: "{apiVersion: v1, kind: ConfigMap}",
			want:  true,
		},
		"flow-style after LF marker": {
			// Helm commonly prefixes with "---\n".
			input: "---\n{apiVersion: v1, kind: ConfigMap}",
			want:  true,
		},
		"flow-style after CRLF marker": {
			// Windows-style line endings before and after the separator.
			input: "---\r\n{apiVersion: v1, kind: ConfigMap}",
			want:  true,
		},
		"flow-style in second doc, LF separator": {
			input: "apiVersion: v1\n---\n{apiVersion: v2, kind: ConfigMap}",
			want:  true,
		},
		"flow-style in second doc, CRLF separator": {
			// "\n---" is a substring of "\r\n---", so CRLF is handled.
			input: "apiVersion: v1\r\n---\r\n{apiVersion: v2, kind: ConfigMap}",
			want:  true,
		},
		"triple-dash not a marker (no trailing whitespace)": {
			// "---foo" is NOT a document-start marker per the YAML spec.
			input: "---foo: bar\n",
			want:  false,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := hasFlowStyleCandidate([]byte(tc.input)); got != tc.want {
				t.Errorf("hasFlowStyleCandidate(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestPostRenderer_Run_FlowStyleYAML(t *testing.T) {
	// Helm v4's kyaml converts JSON output from toJson template functions into
	// YAML flow-style with unquoted keys, e.g. {apiVersion: v1, kind: ConfigMap}.
	// k8s.io/apimachinery ToJSON sees the leading '{' and treats it as JSON,
	// returning it unchanged; json.Unmarshal then fails on the unquoted keys.
	const flowDoc = "{apiVersion: v1, kind: ConfigMap, metadata: {name: test-cm}, data: {key: value}}"
	tests := map[string]struct {
		input string
	}{
		"flow-style with LF document-start marker": {
			// Helm render output commonly starts with "---\n".
			input: "---\n" + flowDoc,
		},
		"flow-style with CRLF document-start marker": {
			input: "---\r\n" + flowDoc,
		},
		"flow-style with no document-start marker": {
			input: flowDoc,
		},
		"block-style followed by flow-style": {
			input: "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: other\n---\n" + flowDoc,
		},
		"flow-style followed by block-style": {
			input: "---\n" + flowDoc + "\n---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: other\n",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			pr := postRender{
				manifest: &manifest.Manifest{Resources: []v1alpha1.BundleResource{}},
				chart:    &chartv2.Chart{},
				opts:     v1alpha1.BundleDeploymentOptions{},
			}

			out, err := pr.Run(bytes.NewBufferString(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			objs, err := yaml.ToObjects(bytes.NewBuffer(out.Bytes()))
			if err != nil {
				t.Fatalf("unexpected error parsing output: %v", err)
			}
			// Every case should produce at least the ConfigMap from the flow-style doc.
			found := false
			for _, obj := range objs {
				if obj.GetObjectKind().GroupVersionKind().Kind == "ConfigMap" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected a ConfigMap in output, got kinds: %v", objectKinds(objs))
			}
		})
	}
}

func objectKinds(objs []kruntime.Object) []string {
	kinds := make([]string, 0, len(objs))
	for _, o := range objs {
		kinds = append(kinds, o.GetObjectKind().GroupVersionKind().Kind)
	}
	return kinds
}

package apply

import (
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func Test_getKindNS(t *testing.T) {
	// TODO add test cases covering templating in name and namespace (less likely in kind)
	// → we should return an empty overwrite in such cases
	br := fleet.BundleResource{
		Name: "folder/my-cm.yaml",
		Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: Foo
  namespace: my-namespace
data:
  bar: baz
  name: {{ .Values.name }}`,
		// No encoding
	}

	or, err := getKindNS(br, "my-bundle")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if or.Kind != "ConfigMap" {
		t.Fatalf("expected ConfigMap kind, got %v", or.Kind)
	}

	if or.Name != "Foo" {
		t.Fatalf("expected name Foo, got %v", or.Name)
	}

	if or.Namespace != "my-namespace" {
		t.Fatalf("expected namespace my-namespace, got %v", or.Namespace)
	}

}

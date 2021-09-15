package target

import (
	"testing"

	"github.com/rancher/wrangler/pkg/yaml"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const bundleYaml = `namespace: default
helm:
  releaseName: labels
  values:
    clusterName: global.fleet.clusterLabels.name
    templateName: "kubernetes.io/cluster/{{ .global.fleet.clusterLabels.name }}"
    templateLogic: "{{ if eq .global.fleet.clusterLabels.envType \"production\" }}Production Workload{{ else }}Non Prod{{ end }}"
    templateMissing: "{{ .missingValue }}"
    customStruct:
      - name: global.fleet.clusterLabels.name
        key1: value1
        key2: value2
      - element1: global.fleet.clusterLabels.envType
      - element2: global.fleet.clusterLabels.name
diff:
  comparePatches:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    name: labels-fleetlabelsdemo
    namespace: default
    operations:
    - op: remove
      path: /spec/rules/0/host
`

func TestProcessLabelValues(t *testing.T) {

	bundle := &v1alpha1.BundleSpec{}

	clusterLabels := make(map[string]string)
	clusterLabels["name"] = "local"
	clusterLabels["envType"] = "dev"

	err := yaml.Unmarshal([]byte(bundleYaml), bundle)
	if err != nil {
		t.Fatalf("error during yaml parsing %v", err)
	}

	err = processLabelValues(bundle.Helm.Values.Data, clusterLabels)
	if err != nil {
		t.Fatalf("error during label processing %v", err)
	}

	clusterName, ok := bundle.Helm.Values.Data["clusterName"]
	if !ok {
		t.Fatal("key clusterName not found")
	}
	if clusterName != "local" {
		t.Fatal("unable to assert correct clusterName")
	}

	customStruct, ok := bundle.Helm.Values.Data["customStruct"].([]interface{})
	if !ok {
		t.Fatal("key customStruct not found")
	}

	firstMap, ok := customStruct[0].(map[string]interface{})
	if !ok {
		t.Fatal("unable to assert first element to map[string]interface{}")
	}
	firstElemVal, ok := firstMap["name"]
	if !ok {
		t.Fatal("unable to find key name in the first element of customStruct")
	}
	if firstElemVal.(string) != "local" {
		t.Fatal("label replacement not performed in first element")
	}

	secondElement, ok := customStruct[1].(map[string]interface{})
	if !ok {
		t.Fatal("unable to assert second element of customStruct to map[string]interface{}")
	}
	secondElemVal, ok := secondElement["element1"]
	if !ok {
		t.Fatal("unable to find key element1")
	}
	if secondElemVal.(string) != "dev" {
		t.Fatal("label replacement not performed in second element")
	}

	thirdElement, ok := customStruct[2].(map[string]interface{})
	if !ok {
		t.Fatal("unable to assert third element of customStruct to map[string]interface{}")
	}
	thirdElemVal, ok := thirdElement["element2"]
	if !ok {
		t.Fatal("unable to find key element2")
	}
	if thirdElemVal.(string) != "local" {
		t.Fatal("label replacement not performed in third element")
	}

	templateName, ok := bundle.Helm.Values.Data["templateName"]
	if !ok {
		t.Fatal("key templateName not found")
	}
	if templateName != "kubernetes.io/cluster/local" {
		t.Fatal("unable to assert correct template")
	}

	templateMissing, ok := bundle.Helm.Values.Data["templateMissing"]
	if !ok {
		t.Fatal("key templateMissing not found")
	}
	if templateMissing != "{{ .missingValue }}" {
		t.Fatal("unable to assert correct templateMising")
	}

	templateLogic, ok := bundle.Helm.Values.Data["templateLogic"]
	if !ok {
		t.Fatal("key templateLogic not found")
	}
	if templateLogic != "Non Prod" {
		t.Fatal("unable to assert correct templateLogic")
	}

}

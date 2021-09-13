package helmdeployer

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	configMapValues, err := processValuesFromObject(configMapName, configMapNamespace, key, nil, &corev1.ConfigMap{
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
	secretValues, err := processValuesFromObject(secretName, secretNamespace, key, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Data: map[string][]byte{
			key: []byte(secretPayload),
		},
	}, nil)
	a.NoError(err)

	totalValues = mergeValues(totalValues, secretValues)
	totalValues = mergeValues(totalValues, configMapValues)
	a.Equal(expected, totalValues)
}

const bundleYaml = `namespace: default
helm:
  releaseName: labels
  values:
    clusterName: global.fleet.clusterLabels.name
    clusterAnnotationName: global.fleet.clusterAnnotations.name
    templateName: "kubernetes.io/cluster/{{ .global.fleet.clusterLabels.name }}"
    templateLogic: "{{ if eq .global.fleet.clusterLabels.envType \"production\" }}Production Workload{{ else }}Non Prod{{ end }}"
    templateMissing: "{{ .missingValue }}"
    nested:
      clusterName: global.fleet.clusterLabels.name
    customStruct:
      - name: global.fleet.clusterLabels.name
        key1: value1
        key2: value2
      - element1: global.fleet.clusterLabels.envType
      - element2: global.fleet.clusterLabels.name
      - element3: "{{ .global.fleet.clusterAnnotations.name }}"
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

	clusterAnnotations := make(map[string]string)
	clusterAnnotations["name"] = "local"
	clusterAnnotations["envType"] = "dev"

	fleetValues := map[string]interface{}{
		"global": map[string]interface{}{
			"fleet": map[string]interface{}{
				"clusterLabels":      clusterLabels,
				"clusterAnnotations": clusterAnnotations,
			},
		},
	}

	err := yaml.Unmarshal([]byte(bundleYaml), bundle)
	if err != nil {
		t.Fatalf("error during yaml parsing %v", err)
	}

	err = processValues(bundle.Helm.Values.Data, clusterLabels, clusterAnnotations, fleetValues)
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

	clusterName, ok = bundle.Helm.Values.Data["nested"].(map[string]interface{})["clusterName"]
	if !ok {
		t.Fatal("key nested.clusterName not found")
	}
	if clusterName != "local" {
		t.Fatal("unable to assert correct nested.clusterName")
	}

	clusterAnnotationName, ok := bundle.Helm.Values.Data["clusterAnnotationName"]
	if !ok {
		t.Fatal("key clusterAnnotationName not found")
	}
	if clusterAnnotationName != "local" {
		t.Fatal("unable to assert correct clusterAnnotationName")
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

	forthElement, ok := customStruct[3].(map[string]interface{})
	if !ok {
		t.Fatal("unable to assert forth element of customStruct to map[string]interface{}")
	}

	forthElemVal, ok := forthElement["element3"]
	if !ok {
		t.Fatal("unable to find key element3")
	}

	if forthElemVal.(string) != "local" {
		t.Fatal("label replacement not performed in forth element")
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

package v1alpha1

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

const legacyResource = `
apiVersion: perfmodel.pea.tppm.tetrapak.com/v1alpha1
id: test
kind: ConfigMap
message: Modified
name: test
perClusterState:
  - clusterId: fleet-default/c-12345
    state: Modified
`

func TestDeserializeResourceLegacyFormat(t *testing.T) {
	var res Resource
	if err := yaml.NewYAMLToJSONDecoder(strings.NewReader(legacyResource)).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if got, want := res.Kind, "ConfigMap"; got != want {
		t.Errorf("resource.kind = %v, wanted %v", got, want)
	}
	var emptyPerClusterState PerClusterState
	if got, want := res.PerClusterState, emptyPerClusterState; !reflect.DeepEqual(got, want) {
		t.Errorf("resource.perClusterState = %v, wanted %v", got, want)
	}

	// Modify the offending field and make sure it gets serialized/deserialized correctly
	res.PerClusterState.Ready = []string{"fleet-default/c-12345"}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(&res); err != nil {
		t.Fatalf("failed to serialize modified resource: %v", err)
	}
	if err := yaml.NewYAMLToJSONDecoder(&buf).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if got, want := len(res.PerClusterState.Ready), 1; got != want {
		t.Errorf("len(resource.perClusterState.Ready) = %v, wanted %v", got, want)
	}
}

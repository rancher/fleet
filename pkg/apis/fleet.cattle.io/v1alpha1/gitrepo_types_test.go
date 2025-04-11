package v1alpha1

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

const compatResource = `
apiVersion: perfmodel.pea.tppm.tetrapak.com/v1alpha1
id: test
kind: ConfigMap
message: Modified
name: test
perClusterState:
  ready:
  - fleet-default/c-12345
`

func TestDeserializeResourceRollbackCompatibility(t *testing.T) {
	var res GitRepoResource
	if err := yaml.NewYAMLToJSONDecoder(strings.NewReader(compatResource)).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if got, want := res.Kind, "ConfigMap"; got != want {
		t.Errorf("resource.kind = %v, wanted %v", got, want)
	}
	if got, want := len(res.PerClusterState), 0; got != want {
		t.Errorf("len(resource.perClusterState) = %v, wanted %v", got, want)
	}

	// Modify the offending field and make sure it gets serialized/deserialized correctly
	res.PerClusterState = []ResourcePerClusterState{
		{
			State:     "Ready",
			ClusterID: "fleet-default/c-12345",
		},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(&res); err != nil {
		t.Fatalf("failed to serialize modified resource: %v", err)
	}
	if err := yaml.NewYAMLToJSONDecoder(&buf).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if got, want := len(res.PerClusterState), 1; got != want {
		t.Errorf("len(resource.perClusterState) = %v, wanted %v", got, want)
	}
	if got, want := res.PerClusterState[0].State, "Ready"; got != want {
		t.Errorf("resource.PerClusterState[0].State = %v, wanted %v", got, want)
	}
}

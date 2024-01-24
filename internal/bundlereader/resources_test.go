package bundlereader

import (
	"bytes"
	"testing"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	valuesOneYaml = `microService1:
  resources:
    limits:
      cpu: 500m
      memory: 500Mi
    requests:
      cpu: 256m
      memory: 256Mi

microService2:
  resources:
    limits:
      cpu: 500m
      memory: 500Mi
    requests:
      cpu: 256m
      memory: 256Mi
`
	valuesTwoYaml = `microService1:
  replicas: 1
microService2:
  replicas: 2`
)

func TestValueMerge(t *testing.T) {
	first := &v1alpha1.GenericMap{}
	second := &v1alpha1.GenericMap{}

	err := yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(valuesOneYaml)).Decode(first)
	if err != nil {
		t.Fatalf("error during valuesOneYaml parsing %v", err)
	}

	err = yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(valuesTwoYaml)).Decode(second)
	if err != nil {
		t.Fatalf("error during valuesTwoYaml parsing %v", err)
	}

	mergeMap := mergeGenericMap(first, second)

	for _, serviceName := range []string{"microService1", "microService2"} {
		serviceVals, ok := mergeMap.Data[serviceName]
		if !ok {
			t.Fatalf("unable to find parent key for service %s", serviceName)
		}
		resourceVals, ok := serviceVals.(map[string]interface{})["resources"]
		if !ok {
			t.Fatalf("unable to find key resources in values for service %s", serviceName)
		}

		limitVals, ok := resourceVals.(map[string]interface{})["limits"]
		if !ok {
			t.Fatalf("unable to find key limits in resources for service %s", serviceName)
		}

		_, ok = limitVals.(map[string]interface{})["cpu"]
		if !ok {
			t.Fatalf("unable to find key cpu in limits for service %s", serviceName)
		}

		_, ok = limitVals.(map[string]interface{})["memory"]
		if !ok {
			t.Fatalf("unable to find key memory in limits for service %s", serviceName)
		}

		requestVals, ok := resourceVals.(map[string]interface{})["requests"]
		if !ok {
			t.Fatalf("unable to find key requests in resources for service %s", serviceName)
		}

		_, ok = requestVals.(map[string]interface{})["cpu"]
		if !ok {
			t.Fatalf("unable to find key cpu in requests for service %s", serviceName)
		}

		_, ok = requestVals.(map[string]interface{})["memory"]
		if !ok {
			t.Fatalf("unable to find key memory in requests for service %s", serviceName)
		}
		_, ok = serviceVals.(map[string]interface{})["replicas"]
		if !ok {
			t.Fatalf("unable to find key replicas in values for service %s", serviceName)
		}
	}
}

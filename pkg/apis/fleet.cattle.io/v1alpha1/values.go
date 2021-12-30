package v1alpha1

import (
	"encoding/json"

	"github.com/rancher/wrangler/pkg/data/convert"
)

// GlobalValues are values that are inserted into deployments containing some metadata about the deployment
type GlobalValues struct {
	Fleet *FleetGlobalValues `json:"fleet,omitempty"`
}

// FleetGlobalValues is metadata pertaining to fleet
type FleetGlobalValues struct {
	// Labels on the target cluster for the given BundleDeployment
	ClusterLabels map[string]string `json:"clusterLabels,omitempty"`
}

type GenericMap struct {
	Data   map[string]interface{} `json:"-"`
	Global *GlobalValues          `json:"global,omitempty"`
}

func (in GenericMap) MarshalJSON() ([]byte, error) {
	return json.Marshal(in.Data)
}

func (in *GenericMap) UnmarshalJSON(data []byte) error {
	in.Data = map[string]interface{}{}
	err := json.Unmarshal(data, &in.Data)
	if err != nil {
		return err
	}

	global, ok := in.Data["global"].(map[string]interface{})
	if !ok {
		return nil
	}

	fleetValues, ok := global["fleet"].(map[string]interface{})
	if !ok {
		return nil
	}

	clusterLabels, ok := fleetValues["clusterLabels"].(map[string]interface{})
	if !ok {
		return nil
	}
	labels := make(map[string]string{}, len(clusterLabels))

	for k, v := range clusterLabels {
		labels[k] = v.(string)
	}

	in.Global = &GlobalValues{
		Fleet: &FleetGlobalValues{
			ClusterLabels: labels,
		},
	}

	return nil
}

func (in *GenericMap) DeepCopyInto(out *GenericMap) {
	out.Data = map[string]interface{}{}
	if err := convert.ToObj(in.Data, &out.Data); err != nil {
		panic(err)
	}

	if err := convert.ToObj(in.Global, &out.Global); err != nil {
		panic(err)
	}
}

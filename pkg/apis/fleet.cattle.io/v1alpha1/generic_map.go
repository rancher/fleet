package v1alpha1

import (
	"encoding/json"
)

type GenericMap struct {
	Data map[string]interface{} `json:"-"`
}

func (in GenericMap) MarshalJSON() ([]byte, error) {
	return json.Marshal(in.Data)
}

func (in *GenericMap) UnmarshalJSON(data []byte) error {
	in.Data = map[string]interface{}{}
	return json.Unmarshal(data, &in.Data)
}

func (in *GenericMap) DeepCopyInto(out *GenericMap) {
	out.Data = make(map[string]interface{}, len(in.Data))
	deepCopyMap(in.Data, out.Data)
}

func deepCopyMap(src, dest map[string]interface{}) {
	for key := range src {
		switch value := src[key].(type) {
		case map[string]interface{}:
			destValue := make(map[string]interface{}, len(value))
			deepCopyMap(value, destValue)
			dest[key] = destValue
		default:
			dest[key] = value
		}
	}
}

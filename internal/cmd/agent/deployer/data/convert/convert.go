package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func singular(value interface{}) interface{} {
	if slice, ok := value.([]string); ok {
		if len(slice) == 0 {
			return nil
		}
		return slice[0]
	}
	if slice, ok := value.([]interface{}); ok {
		if len(slice) == 0 {
			return nil
		}
		return slice[0]
	}
	return value
}

func toStringNoTrim(value interface{}) string {
	if t, ok := value.(time.Time); ok {
		return t.Format(time.RFC3339)
	}
	single := singular(value)
	if single == nil {
		return ""
	}
	return fmt.Sprint(single)
}

func ToString(value interface{}) string {
	return strings.TrimSpace(toStringNoTrim(value))
}

func ToTimestamp(value interface{}) (int64, error) {
	str := ToString(value)
	if str == "" {
		return 0, errors.New("invalid date")
	}
	t, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return 0, err
	}
	return t.UnixNano() / 1000000, nil
}

func ToBool(value interface{}) bool {
	value = singular(value)

	b, ok := value.(bool)
	if ok {
		return b
	}

	str := strings.ToLower(ToString(value))
	return str == "true" || str == "t" || str == "yes" || str == "y"
}

func ToInterfaceSlice(obj interface{}) []interface{} {
	if v, ok := obj.([]interface{}); ok {
		return v
	}
	return nil
}

func ToMapSlice(obj interface{}) []map[string]interface{} {
	if v, ok := obj.([]map[string]interface{}); ok {
		return v
	}
	vs, _ := obj.([]interface{})
	var result []map[string]interface{}
	for _, item := range vs {
		if v, ok := item.(map[string]interface{}); ok {
			result = append(result, v)
		} else {
			return nil
		}
	}

	return result
}

func ToStringSlice(data interface{}) []string {
	if v, ok := data.([]string); ok {
		return v
	}
	if v, ok := data.([]interface{}); ok {
		var result []string
		for _, item := range v {
			result = append(result, ToString(item))
		}
		return result
	}
	if v, ok := data.(string); ok {
		return []string{v}
	}
	return nil
}

func ToMapInterface(obj interface{}) map[string]interface{} {
	v, _ := obj.(map[string]interface{})
	return v
}

func ToObj(data interface{}, into interface{}) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, into)
}

func EncodeToMap(obj interface{}) (map[string]interface{}, error) {
	if m, ok := obj.(map[string]interface{}); ok {
		return m, nil
	}

	if unstr, ok := obj.(*unstructured.Unstructured); ok {
		return unstr.Object, nil
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{}
	dec := json.NewDecoder(bytes.NewBuffer(b))
	dec.UseNumber()
	return result, dec.Decode(&result)
}

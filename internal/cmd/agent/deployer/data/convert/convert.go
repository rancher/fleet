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

func singular(value any) any {
	if slice, ok := value.([]string); ok {
		if len(slice) == 0 {
			return nil
		}
		return slice[0]
	}
	if slice, ok := value.([]any); ok {
		if len(slice) == 0 {
			return nil
		}
		return slice[0]
	}
	return value
}

func toStringNoTrim(value any) string {
	if t, ok := value.(time.Time); ok {
		return t.Format(time.RFC3339)
	}
	single := singular(value)
	if single == nil {
		return ""
	}
	return fmt.Sprint(single)
}

func ToString(value any) string {
	return strings.TrimSpace(toStringNoTrim(value))
}

func ToTimestamp(value any) (int64, error) {
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

func ToBool(value any) bool {
	value = singular(value)

	b, ok := value.(bool)
	if ok {
		return b
	}

	str := strings.ToLower(ToString(value))
	return str == "true" || str == "t" || str == "yes" || str == "y"
}

func ToInterfaceSlice(obj any) []any {
	if v, ok := obj.([]any); ok {
		return v
	}
	return nil
}

func ToMapSlice(obj any) []map[string]any {
	if v, ok := obj.([]map[string]any); ok {
		return v
	}
	vs, _ := obj.([]any)
	var result []map[string]any
	for _, item := range vs {
		if v, ok := item.(map[string]any); ok {
			result = append(result, v)
		} else {
			return nil
		}
	}

	return result
}

func ToStringSlice(data any) []string {
	if v, ok := data.([]string); ok {
		return v
	}
	if v, ok := data.([]any); ok {
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

func ToMapInterface(obj any) map[string]any {
	v, _ := obj.(map[string]any)
	return v
}

func ToObj(data any, into any) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, into)
}

func EncodeToMap(obj any) (map[string]any, error) {
	if m, ok := obj.(map[string]any); ok {
		return m, nil
	}

	if unstr, ok := obj.(*unstructured.Unstructured); ok {
		return unstr.Object, nil
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	dec := json.NewDecoder(bytes.NewBuffer(b))
	dec.UseNumber()
	return result, dec.Decode(&result)
}

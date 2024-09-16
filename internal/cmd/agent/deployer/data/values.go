// Package data contains functions for working with unstructured values like []interface or map[string]interface{}.
// It allows reading/writing to these values without having to convert to structured items.
package data

func GetValueN(data map[string]interface{}, keys ...string) interface{} {
	val, _ := getValue(data, keys...)
	return val
}

// getValue works similar to GetValueFromAny, but can only process maps. Kept this way to avoid breaking changes with
// the previous interface, GetValueFromAny should be used in most cases since that can handle slices as well.
func getValue(data map[string]interface{}, keys ...string) (interface{}, bool) {
	for i, key := range keys {
		if i == len(keys)-1 {
			val, ok := data[key]
			return val, ok
		}
		data, _ = data[key].(map[string]interface{})
	}
	return nil, false
}

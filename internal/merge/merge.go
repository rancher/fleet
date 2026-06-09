// Package merge provides generic helpers for merging slices by identity key.
package merge

// ByKey merges two slices by identity key. For entries with a non-empty key
// (returned by keyFn), a custom entry that shares a key with a base entry
// triggers onCollide(base, custom); the return value replaces the base entry.
// Custom entries with no matching base key are appended. Non-keyed entries
// (empty key) are always appended. Duplicate keys within custom are collapsed;
// the first occurrence takes effect.
func ByKey[T any](base, custom []T, keyFn func(T) string, onCollide func(base, custom T) T) []T {
	if len(custom) == 0 {
		return base
	}
	baseIndex := make(map[string]int, len(base))
	for i, v := range base {
		if k := keyFn(v); k != "" {
			baseIndex[k] = i
		}
	}
	result := make([]T, len(base), len(base)+len(custom))
	copy(result, base)
	seen := make(map[string]struct{}, len(custom))
	for _, v := range custom {
		k := keyFn(v)
		if k != "" {
			if _, done := seen[k]; done {
				continue
			}
			seen[k] = struct{}{}
			if idx, exists := baseIndex[k]; exists {
				result[idx] = onCollide(result[idx], v)
				continue
			}
		}
		result = append(result, v)
	}
	return result
}

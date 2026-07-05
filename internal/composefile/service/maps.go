package service

import (
	"sort"
	"strings"
)

// AsMap normalizes YAML/JSON object values to map[string]any.
func AsMap(raw any) (map[string]any, bool) {
	switch typed := raw.(type) {
	case map[string]any:
		return typed, true
	case map[any]any:
		out := map[string]any{}
		for k, v := range typed {
			key, ok := stringMapKey(k)
			if !ok {
				return nil, false
			}
			out[key] = v
		}
		return out, true
	default:
		return nil, false
	}
}

// stringMapKeys returns keys from a string-keyed map.
func stringMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

// sortedStringKeys returns deterministic string-keyed map iteration order.
func sortedStringKeys(values map[string]any) []string {
	keys := stringMapKeys(values)
	sort.Strings(keys)
	return keys
}

// stringMapKey returns a trimmed key only when a loose map key is a string.
func stringMapKey(raw any) (string, bool) {
	key, ok := raw.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(key), true
}

package template

import "strings"

// asMap normalizes YAML/JSON object values to map[string]any for template nodes.
func asMap(raw any) (map[string]any, bool) {
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

// stringMapKey returns a trimmed key only when a loose map key is a string.
func stringMapKey(raw any) (string, bool) {
	key, ok := raw.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(key), true
}

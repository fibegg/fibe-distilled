package composefile

import (
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
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

// MutationMap decodes a Compose document for YAML-preserving mutation.
func MutationMap(composeYAML string) (map[string]any, error) {
	if err := validateRawComposeShapes(composeYAML); err != nil {
		return nil, err
	}
	var rendered map[string]any
	if err := yaml.Unmarshal([]byte(composeYAML), &rendered); err != nil {
		return nil, err
	}
	if rendered == nil {
		return nil, errors.New("compose yaml must be a mapping")
	}
	return rendered, nil
}

// MutationYAML serializes a mutated Compose document.
func MutationYAML(rendered map[string]any) (string, error) {
	out, err := yaml.Marshal(rendered)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// stringMapKey returns a trimmed key only when a loose map key is a string.
func stringMapKey(raw any) (string, bool) {
	key, ok := raw.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(key), true
}

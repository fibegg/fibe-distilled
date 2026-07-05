package service

import (
	"fmt"
	"maps"
	"strings"
)

// ApplyGlobalEnv applies launch env overrides to every normal service.
func ApplyGlobalEnv(rendered map[string]any, env map[string]string) error {
	if len(env) == 0 {
		return nil
	}
	services, ok := AsMap(rendered["services"])
	if !ok {
		return nil
	}
	values := map[string]any{}
	for key, value := range env {
		values[key] = value
	}
	return applyGlobalEnvAny(services, values)
}

// applyGlobalEnvAny applies normalized env values to every normal service.
func applyGlobalEnvAny(services map[string]any, env map[string]any) error {
	if len(env) == 0 {
		return nil
	}
	for name, raw := range services {
		serviceMap, ok := globalEnvTargetService(name, raw)
		if !ok {
			continue
		}
		if err := applyEnvOverrideValues(name, serviceMap, env); err != nil {
			return err
		}
	}
	return nil
}

// globalEnvTargetService returns normal services eligible for global env values.
func globalEnvTargetService(name string, raw any) (map[string]any, bool) {
	if name == globalServiceOverrideKey || strings.HasPrefix(name, "_") {
		return nil, false
	}
	return AsMap(raw)
}

// applyEnvOverrideValues writes normalized env override values onto a service.
func applyEnvOverrideValues(serviceName string, serviceMap map[string]any, env map[string]any) error {
	serviceEnv, err := ensureEnvironmentMap(serviceName, serviceMap)
	if err != nil {
		return err
	}
	for key, value := range env {
		if normalized, ok := envOverrideString(value); ok {
			serviceEnv[key] = normalized
		}
	}
	return nil
}

// ensureEnvironmentMap returns a service environment map without dropping list entries.
func ensureEnvironmentMap(serviceName string, serviceMap map[string]any) (map[string]any, error) {
	if existing, ok, err := environmentMap(serviceName, serviceMap["environment"]); ok || err != nil {
		if err != nil {
			return nil, err
		}
		serviceMap["environment"] = existing
		return existing, nil
	}
	next := map[string]any{}
	serviceMap["environment"] = next
	return next, nil
}

// environmentMap converts supported Compose environment shapes to a map.
func environmentMap(serviceName string, raw any) (map[string]any, bool, error) {
	switch typed := raw.(type) {
	case map[string]any:
		return typed, true, nil
	case map[any]any:
		return looseEnvironmentObjectMap(serviceName, typed)
	case []any:
		return environmentAnyListMap(serviceName, typed)
	case []string:
		return environmentStringListMap(typed), true, nil
	default:
		return nil, false, nil
	}
}

// looseEnvironmentObjectMap converts a YAML loose map to environment entries.
func looseEnvironmentObjectMap(serviceName string, values map[any]any) (map[string]any, bool, error) {
	out := make(map[string]any, len(values))
	for key, item := range values {
		text, ok := stringMapKey(key)
		if !ok {
			return nil, false, fmt.Errorf("compose service %q environment map keys must be strings", serviceName)
		}
		out[text] = item
	}
	return out, true, nil
}

// environmentAnyListMap converts YAML list-form environment entries to a map.
func environmentAnyListMap(serviceName string, values []any) (map[string]any, bool, error) {
	out := map[string]any{}
	for idx, value := range values {
		if err := addEnvironmentListValue(serviceName, idx, out, value); err != nil {
			return nil, false, err
		}
	}
	return out, true, nil
}

// environmentStringListMap converts string list-form environment entries to a map.
func environmentStringListMap(values []string) map[string]any {
	out := map[string]any{}
	for _, value := range values {
		addEnvironmentString(out, value)
	}
	return out
}

// addEnvironmentListValue adds one YAML-decoded environment list item.
func addEnvironmentListValue(serviceName string, idx int, out map[string]any, value any) error {
	switch typed := value.(type) {
	case string:
		addEnvironmentString(out, typed)
		return nil
	case map[string]any:
		maps.Copy(out, typed)
		return nil
	case map[any]any:
		for key, item := range typed {
			text, ok := stringMapKey(key)
			if !ok {
				return fmt.Errorf("compose service %q environment[%d] map keys must be strings", serviceName, idx)
			}
			out[text] = item
		}
		return nil
	default:
		return fmt.Errorf("compose service %q environment[%d] must be a string or string-keyed map", serviceName, idx)
	}
}

// addEnvironmentString adds one KEY or KEY=value Compose environment entry.
func addEnvironmentString(out map[string]any, raw string) {
	key, value, hasValue := strings.Cut(raw, "=")
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if hasValue {
		out[key] = value
		return
	}
	out[key] = nil
}

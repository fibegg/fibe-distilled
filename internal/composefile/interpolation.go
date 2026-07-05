package composefile

import "regexp"

// composeEnvExpression matches the Compose ${NAME} and ${NAME:-default} forms.
var composeEnvExpression = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-])([^}]*))?\}`)

// ApplyEnvInterpolation replaces Compose environment expressions whose values
// are supplied by fibe env overrides while leaving unrelated expressions intact.
func ApplyEnvInterpolation(rendered map[string]any, env map[string]string) {
	if len(env) == 0 {
		return
	}
	interpolateEnvAny(rendered, env)
}

// interpolateEnvAny recursively applies environment interpolation to strings.
func interpolateEnvAny(value any, env map[string]string) any {
	switch typed := value.(type) {
	case map[string]any:
		return interpolateEnvStringMap(typed, env)
	case map[any]any:
		return interpolateEnvAnyMap(typed, env)
	case []any:
		return interpolateEnvAnySlice(typed, env)
	case []string:
		return interpolateEnvStringSlice(typed, env)
	case string:
		return interpolateEnvString(typed, env)
	default:
		return typed
	}
}

// interpolateEnvStringMap applies interpolation to a string-keyed YAML map.
func interpolateEnvStringMap(value map[string]any, env map[string]string) map[string]any {
	for key, item := range value {
		value[key] = interpolateEnvAny(item, env)
	}
	return value
}

// interpolateEnvAnyMap applies interpolation to a generic YAML map.
func interpolateEnvAnyMap(value map[any]any, env map[string]string) map[any]any {
	for key, item := range value {
		value[key] = interpolateEnvAny(item, env)
	}
	return value
}

// interpolateEnvAnySlice applies interpolation to a generic YAML sequence.
func interpolateEnvAnySlice(value []any, env map[string]string) []any {
	for idx, item := range value {
		value[idx] = interpolateEnvAny(item, env)
	}
	return value
}

// interpolateEnvStringSlice applies interpolation to a string sequence.
func interpolateEnvStringSlice(value []string, env map[string]string) []string {
	for idx, item := range value {
		value[idx] = interpolateEnvString(item, env)
	}
	return value
}

// interpolateEnvString replaces supported Compose environment expressions.
func interpolateEnvString(value string, env map[string]string) string {
	return composeEnvExpression.ReplaceAllStringFunc(value, func(expression string) string {
		matches := composeEnvExpression.FindStringSubmatch(expression)
		if len(matches) != 4 {
			return expression
		}
		envValue, ok := env[matches[1]]
		if !ok {
			return expression
		}
		if matches[2] == ":-" && envValue == "" {
			return matches[3]
		}
		return envValue
	})
}

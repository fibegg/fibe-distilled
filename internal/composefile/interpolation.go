package composefile

import "regexp"

var composeEnvExpression = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-])([^}]*))?\}`)

// ApplyEnvInterpolation replaces Compose environment expressions whose values
// are supplied by fibe env overrides while leaving unrelated expressions intact.
func ApplyEnvInterpolation(rendered map[string]any, env map[string]string) {
	if len(env) == 0 {
		return
	}
	interpolateEnvAny(rendered, env)
}

func interpolateEnvAny(value any, env map[string]string) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = interpolateEnvAny(item, env)
		}
		return typed
	case map[any]any:
		for key, item := range typed {
			typed[key] = interpolateEnvAny(item, env)
		}
		return typed
	case []any:
		for idx, item := range typed {
			typed[idx] = interpolateEnvAny(item, env)
		}
		return typed
	case []string:
		for idx, item := range typed {
			typed[idx] = interpolateEnvString(item, env)
		}
		return typed
	case string:
		return interpolateEnvString(typed, env)
	default:
		return typed
	}
}

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

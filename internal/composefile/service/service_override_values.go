package service

import (
	"math"
	"strconv"
	"strings"
)

// isStringValue reports whether a value is a nonblank string.
func isStringValue(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

// isStartCommandValue reports whether a command override shape is accepted.
func isStartCommandValue(value any) bool {
	_, ok := normalizeStartCommand(value)
	return ok
}

// isBoolOverrideValue reports accepted boolean override types.
func isBoolOverrideValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "false":
			return true
		}
	}
	return false
}

// portEndpointText converts supported port endpoint values to text.
func portEndpointText(value any) (string, bool) {
	text, ok := scalarPortEndpointText(value)
	if !ok || !validPortEndpoint(text) {
		return "", false
	}
	return text, true
}

// scalarPortEndpointText formats JSON/YAML scalar port endpoint values.
func scalarPortEndpointText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case float64:
		if typed != float64(int64(typed)) {
			return "", false
		}
		return strconv.FormatInt(int64(typed), 10), true
	default:
		return "", false
	}
}

// overrideString reads a scalar override as trimmed text.
func overrideString(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

// overridePortString reads a service override port endpoint as text.
func overridePortString(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := portEndpointText(value)
	if !ok {
		return ""
	}
	return text
}

// envOverrideString formats supported environment override scalar values.
func envOverrideString(value any) (string, bool) {
	if text, ok := basicEnvOverrideString(value); ok {
		return text, true
	}
	if text, ok := signedEnvOverrideString(value); ok {
		return text, true
	}
	if text, ok := unsignedEnvOverrideString(value); ok {
		return text, true
	}
	return floatEnvOverrideString(value)
}

// basicEnvOverrideString formats string and boolean environment scalars.
func basicEnvOverrideString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

// signedEnvOverrideString formats signed integer environment scalars.
func signedEnvOverrideString(value any) (string, bool) {
	switch typed := value.(type) {
	case int:
		return strconv.Itoa(typed), true
	case int8:
		return strconv.FormatInt(int64(typed), 10), true
	case int16:
		return strconv.FormatInt(int64(typed), 10), true
	case int32:
		return strconv.FormatInt(int64(typed), 10), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	default:
		return "", false
	}
}

// unsignedEnvOverrideString formats unsigned integer environment scalars.
func unsignedEnvOverrideString(value any) (string, bool) {
	switch typed := value.(type) {
	case uint:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	default:
		return "", false
	}
}

// floatEnvOverrideString formats finite float environment scalars.
func floatEnvOverrideString(value any) (string, bool) {
	switch typed := value.(type) {
	case float32:
		return finiteFloatString(float64(typed), 32)
	case float64:
		return finiteFloatString(typed, 64)
	default:
		return "", false
	}
}

// finiteFloatString formats a float only when YAML/JSON can represent it.
func finiteFloatString(value float64, bitSize int) (string, bool) {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return "", false
	}
	return strconv.FormatFloat(value, 'f', -1, bitSize), true
}

// formatFloatScalar preserves fractional YAML/JSON numbers for validators.
func formatFloatScalar(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// boolParseResult carries a boolean value and whether one was parsed.
type boolParseResult struct {
	value bool
	ok    bool
}

// overrideBool reads a strict boolean override value.
func overrideBool(values map[string]any, key string) boolParseResult {
	value, ok := values[key]
	if !ok || value == nil {
		return boolParseResult{}
	}
	switch typed := value.(type) {
	case bool:
		return boolParseResult{value: typed, ok: true}
	case string:
		return parseStrictBool(typed)
	default:
		return boolParseResult{}
	}
}

// parseStrictBool accepts only true or false.
func parseStrictBool(raw string) boolParseResult {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return boolParseResult{value: true, ok: true}
	case "false":
		return boolParseResult{ok: true}
	default:
		return boolParseResult{}
	}
}

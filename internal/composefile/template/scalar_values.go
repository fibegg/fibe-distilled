package template

import (
	"math"
	"strconv"
	"strings"
)

// templateScalarString converts supported template scalar defaults to strings.
func templateScalarString(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "", false
	case string:
		return typed, typed != ""
	case bool:
		return strconv.FormatBool(typed), true
	case int, int8, int16, int32, int64:
		return signedTemplateNumberString(typed), true
	case uint, uint8, uint16, uint32, uint64:
		return unsignedTemplateNumberString(typed), true
	case float32, float64:
		return floatTemplateNumberString(typed), true
	default:
		return "", false
	}
}

// templateScalarText converts a resolved template scalar to inline text.
func templateScalarText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		text, _ := templateScalarString(typed)
		return text
	}
}

// signedTemplateNumberString formats signed integer template values.
func signedTemplateNumberString(value any) string {
	switch typed := value.(type) {
	case int:
		return strconv.Itoa(typed)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}

// unsignedTemplateNumberString formats unsigned integer template values.
func unsignedTemplateNumberString(value any) string {
	switch typed := value.(type) {
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	default:
		return ""
	}
}

// floatTemplateNumberString formats floating template values.
func floatTemplateNumberString(value any) string {
	switch typed := value.(type) {
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

// finiteTemplateScalar reports whether numeric scalar values are finite.
func finiteTemplateScalar(value any) bool {
	switch typed := value.(type) {
	case float32:
		return !math.IsNaN(float64(typed)) && !math.IsInf(float64(typed), 0)
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	default:
		return true
	}
}

// isTemplateLiteralScalar reports whether a value is an allowed default literal.
func isTemplateLiteralScalar(value any) bool {
	switch value.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return finiteTemplateScalar(value)
	default:
		return false
	}
}

// coerceTemplateScalar converts provided strings to simple YAML scalars.
func coerceTemplateScalar(value string) any {
	if value == "" {
		return ""
	}
	if strings.EqualFold(value, "true") {
		return true
	}
	if strings.EqualFold(value, "false") {
		return false
	}
	if isUnsignedDigitString(value) {
		i, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return value
		}
		return i
	}
	return value
}

// isUnsignedDigitString reports whether a path/index string is numeric.
func isUnsignedDigitString(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

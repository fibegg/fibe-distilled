package request

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// StringScalarMapResult carries decoded scalar-map values or validation text.
type StringScalarMapResult struct {
	// Values is the decoded object when valid.
	Values map[string]string
	// Invalid is a client-visible validation message when decoding failed.
	Invalid string
}

// DecodeJSONStringScalarMap converts a JSON object with scalar values to strings.
func DecodeJSONStringScalarMap(raw json.RawMessage, field string) StringScalarMapResult {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return StringScalarMapResult{Invalid: field + " must be an object"}
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return StringScalarMapResult{Invalid: field + " must be an object"}
	}
	out := make(map[string]string, len(values))
	for key, rawValue := range values {
		value, ok := decodeJSONStringScalar(rawValue)
		if !ok {
			return StringScalarMapResult{Invalid: field + "." + key + " must be a string, number, or boolean"}
		}
		out[key] = value
	}
	return StringScalarMapResult{Values: out}
}

// ValidateStringMapField validates an optional decoded string-map payload field.
func ValidateStringMapField(present bool, values map[string]string, field string, allowEmpty bool) error {
	if !present {
		return nil
	}
	if values == nil {
		return fmt.Errorf("%s must be an object", field)
	}
	if !allowEmpty && len(values) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	if hasBlankStringMapKey(values) {
		return fmt.Errorf("%s keys must not be blank", field)
	}
	return nil
}

// ValidateNonEmptyObjectMapField validates an optional non-empty object payload field.
func ValidateNonEmptyObjectMapField(present bool, values map[string]any, field string) error {
	if !present {
		return nil
	}
	if values == nil {
		return fmt.Errorf("%s must be an object", field)
	}
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	return nil
}

// hasBlankStringMapKey reports whether a decoded JSON object has a blank key.
func hasBlankStringMapKey(values map[string]string) bool {
	for key := range values {
		if strings.TrimSpace(key) == "" {
			return true
		}
	}
	return false
}

// decodeJSONStringScalar converts one JSON scalar to the Fibe variable string form.
func decodeJSONStringScalar(raw json.RawMessage) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case json.Number:
		return jsonNumberString(typed), true
	default:
		return "", false
	}
}

// jsonNumberString formats JSON numbers like FibeCore's scalar stringification.
func jsonNumberString(number json.Number) string {
	if integer, err := number.Int64(); err == nil {
		return strconv.FormatInt(integer, 10)
	}
	floating, err := number.Float64()
	if err != nil || math.IsInf(floating, 0) {
		return number.String()
	}
	return strconv.FormatFloat(floating, 'f', -1, 64)
}

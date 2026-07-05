package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
)

// ErrPayloadObject reports a payload position that must be a JSON object.
var ErrPayloadObject = errors.New("payload must be a JSON object")

// JSONFields tracks top-level JSON field presence.
type JSONFields map[string]struct{}

// DecodeJSONFields returns top-level fields in a JSON object.
func DecodeJSONFields(data []byte) (JSONFields, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, ErrPayloadObject
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrPayloadObject
	}
	fields := make(JSONFields, len(raw))
	for key := range raw {
		fields[key] = struct{}{}
	}
	return fields, nil
}

// DecodePayloadFields decodes a payload and returns field presence.
func DecodePayloadFields(data []byte, dst any) (JSONFields, error) {
	if err := json.Unmarshal(data, dst); err != nil {
		return nil, err
	}
	return DecodeJSONFields(data)
}

// DecodePayloadInto decodes JSON into an alias type and records fields.
func DecodePayloadInto[T any](data []byte, dst *T, fields *JSONFields) error {
	decodedFields, err := DecodePayloadFields(data, dst)
	if err != nil {
		return err
	}
	*fields = decodedFields
	return nil
}

// Has reports whether a JSON field was present.
func (f JSONFields) Has(name string) bool {
	_, ok := f[name]
	return ok
}

// HasAny reports whether any JSON field was present.
func (f JSONFields) HasAny(names ...string) bool {
	return slices.ContainsFunc(names, f.Has)
}

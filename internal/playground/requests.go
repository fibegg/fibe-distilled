package playground

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/request"
)

// jsonFields aliases shared JSON field-presence tracking.
type jsonFields = request.JSONFields

// decode decodes one required JSON request body.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return request.Decode(w, r, dst)
}

// decodeOptional decodes a JSON body that may be empty.
func decodeOptional(w http.ResponseWriter, r *http.Request, dst any) bool {
	return request.DecodeOptional(w, r, dst)
}

// decodePayloadInto decodes JSON into an alias type and records fields.
func decodePayloadInto[T any](data []byte, dst *T, fields *jsonFields) error {
	return request.DecodePayloadInto(data, dst, fields)
}

// decodePayloadFields decodes a payload and returns field presence.
func decodePayloadFields(data []byte, dst any) (jsonFields, error) {
	return request.DecodePayloadFields(data, dst)
}

// presentBlankString reports a present string field that is blank.
func presentBlankString(fields jsonFields, name string, value string) bool {
	return fields.Has(name) && strings.TrimSpace(value) == ""
}

// presentBlankStringPtr reports a present optional string field that is blank.
func presentBlankStringPtr(fields jsonFields, name string, value *string) bool {
	return fields.Has(name) && (value == nil || strings.TrimSpace(*value) == "")
}

// blankJSONReference reports explicit blank string references.
func blankJSONReference(raw json.RawMessage) bool {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return false
	}
	var text string
	return json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) == ""
}

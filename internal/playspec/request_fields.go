package playspec

import (
	"strconv"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/request"
)

// jsonFields tracks top-level JSON field presence.
type jsonFields = request.JSONFields

// decodePayloadInto decodes JSON into an alias type and records fields.
func decodePayloadInto[T any](data []byte, dst *T, fields *jsonFields) error {
	return request.DecodePayloadInto(data, dst, fields)
}

// presentBlankString reports a present string field that is blank.
func presentBlankString(fields jsonFields, name string, value string) bool {
	return fields.Has(name) && strings.TrimSpace(value) == ""
}

// normalizeScalarInput trims surrounding whitespace from accepted text fields.
func normalizeScalarInput(raw string) string {
	return strings.TrimSpace(raw)
}

// addNonEmptyMapValue writes value only when it is not blank.
func addNonEmptyMapValue(values map[string]any, key string, value string) {
	if value != "" {
		values[key] = value
	}
}

// idString formats positive persisted IDs for name-or-ID lookups.
func idString(id int64) string {
	return strconv.FormatInt(id, 10)
}

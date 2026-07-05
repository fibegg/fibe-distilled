package playground

import (
	"strconv"
	"strings"
)

// normalizeScalarInput trims surrounding whitespace from accepted text fields.
func normalizeScalarInput(raw string) string {
	return strings.TrimSpace(raw)
}

// idString formats positive persisted IDs for name-or-ID store lookups.
func idString(id int64) string {
	return strconv.FormatInt(id, 10)
}

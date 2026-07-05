package service

import "strings"

// isBlank reports whether a string is empty after trimming.
func isBlank(value string) bool {
	return strings.TrimSpace(value) == ""
}

// hasBlankMapKey reports whether a string-keyed map has a blank key.
func hasBlankMapKey(values map[string]any) bool {
	for key := range values {
		if strings.TrimSpace(key) == "" {
			return true
		}
	}
	return false
}

// cleanUniqueStrings trims strings and preserves first occurrence order.
func cleanUniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

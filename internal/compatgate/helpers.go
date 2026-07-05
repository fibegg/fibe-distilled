package compatgate

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const (
	// excludedAutomationResource names the hard-excluded automation API family at the compatibility boundary.
	excludedAutomationResource = "agent"
	// excludedGitProviderName names the hard-excluded managed Git provider at the compatibility boundary.
	excludedGitProviderName = "gitea"
)

// unwrap returns a nested resource payload or the original body.
func unwrap(body map[string]any, root string) map[string]any {
	if body == nil {
		return nil
	}
	if nested, ok := body[root].(map[string]any); ok {
		return nested
	}
	return body
}

// appendIfPresent adds an unsupported field when the key exists.
func appendIfPresent(out []unsupportedItem, payload map[string]any, field string, reason string) []unsupportedItem {
	if payload == nil {
		return out
	}
	if _, ok := payload[field]; !ok {
		return out
	}
	return append(out, unsupportedItem{Key: "field:" + field, Reason: reason})
}

// appendIfTrue adds an unsupported field when the value is truthy.
func appendIfTrue(out []unsupportedItem, payload map[string]any, field string, reason string) []unsupportedItem {
	if payload == nil {
		return out
	}
	value, ok := payload[field]
	if !ok {
		return out
	}
	if truthyField(value) {
		return append(out, unsupportedItem{Key: "field:" + field, Reason: reason})
	}
	return out
}

// truthyField recognizes boolean-like JSON values.
func truthyField(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	case json.Number:
		return truthyJSONNumber(typed)
	default:
		return false
	}
}

// truthyJSONNumber recognizes non-zero JSON numbers from the gate decoder.
func truthyJSONNumber(value json.Number) bool {
	parsed, err := strconv.ParseFloat(value.String(), 64)
	return err == nil && parsed != 0
}

// stringValue extracts a trimmed string from a JSON value.
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// propProviderUnsupportedItem classifies Prop providers outside fibe-distilled scope.
func propProviderUnsupportedItem(raw string, prefix string) (unsupportedItem, bool) {
	provider := strings.ToLower(strings.TrimSpace(raw))
	switch provider {
	case "", "github", "git":
		return unsupportedItem{}, false
	case excludedGitProviderName:
		return unsupportedItem{
			Key:    prefix + excludedGitProviderName,
			Reason: "this git provider is excluded from fibe-distilled; use provider=github, provider=git, or omit provider",
		}, true
	default:
		return unsupportedItem{
			Key:    prefix + provider,
			Reason: "this Prop provider is not implemented in fibe-distilled; use provider=github, provider=git, or omit provider",
		}, true
	}
}

// requestBodyPresent reports whether a request carries an explicit body.
func requestBodyPresent(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		return true
	}
	return r.ContentLength < 0 && r.Body != nil && r.Body != http.NoBody
}

// sortUnsupported orders rejected fields for stable error details.
func sortUnsupported(items []unsupportedItem) {
	sort.Slice(items, func(i, j int) bool {
		return strings.Compare(items[i].Key, items[j].Key) < 0
	})
}

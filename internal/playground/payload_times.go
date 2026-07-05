package playground

import (
	"strings"
	"time"
)

const (
	// minPayloadUnixTimestamp is the earliest Unix second JSON can encode.
	minPayloadUnixTimestamp = -62167219200
	// maxPayloadUnixTimestamp is the latest Unix second JSON can encode.
	maxPayloadUnixTimestamp = 253402300799
)

// parsePayloadTime parses RFC3339 strings or integer Unix timestamps.
func parsePayloadTime(value any) (*time.Time, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, true
	case string:
		return parsePayloadTimeString(typed)
	case float64:
		return parsePayloadTimeFloat(typed)
	case int64:
		return unixPayloadTime(typed)
	case time.Time:
		return payloadTime(typed)
	}
	return nil, false
}

// parsePayloadTimeString parses a nonblank RFC3339 timestamp string.
func parsePayloadTimeString(value string) (*time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return payloadTime(parsed)
		}
	}
	return nil, false
}

// parsePayloadTimeFloat parses a JSON number as integer Unix seconds.
func parsePayloadTimeFloat(value float64) (*time.Time, bool) {
	if value < float64(minPayloadUnixTimestamp) || value > float64(maxPayloadUnixTimestamp) {
		return nil, false
	}
	seconds := int64(value)
	if value != float64(seconds) {
		return nil, false
	}
	return unixPayloadTime(seconds)
}

// unixPayloadTime converts a JSON-safe Unix timestamp.
func unixPayloadTime(seconds int64) (*time.Time, bool) {
	if seconds < minPayloadUnixTimestamp || seconds > maxPayloadUnixTimestamp {
		return nil, false
	}
	parsed := time.Unix(seconds, 0).UTC()
	return payloadTime(parsed)
}

// payloadTime accepts only times that can be encoded in API JSON responses.
func payloadTime(value time.Time) (*time.Time, bool) {
	if _, err := value.MarshalJSON(); err != nil {
		return nil, false
	}
	return &value, true
}

package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// nullableString converts an optional string to a nullable SQL argument.
func nullableString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// nullableInt64 converts an optional int64 to a nullable SQL argument.
func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// nullableTime converts an optional time to a nullable encoded SQL argument.
func nullableTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return encodeTime(*p)
}

// boolValue encodes an optional bool as SQLite 0/1.
func boolValue(p *bool) int {
	if p != nil && *p {
		return 1
	}
	return 0
}

// boolToInt encodes a bool as SQLite 0/1.
func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// stringPtr converts a nullable SQL string to an optional string.
func stringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

// int64Ptr converts a nullable SQL int64 to an optional int64.
func int64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// parseNullableStoredTime decodes an optional RFC3339Nano timestamp.
func parseNullableStoredTime(field string, v sql.NullString) (time.Time, bool, error) {
	if !v.Valid || v.String == "" {
		return time.Time{}, false, nil
	}
	t, err := parseStoredTime(field, v.String)
	if err != nil {
		return time.Time{}, true, err
	}
	return t, true, nil
}

// assignNullableStoredTime decodes and assigns an optional timestamp pointer.
func assignNullableStoredTime(field string, v sql.NullString, target **time.Time) error {
	parsed, ok, err := parseNullableStoredTime(field, v)
	if err != nil {
		return err
	}
	if !ok {
		*target = nil
		return nil
	}
	*target = new(parsed)
	return nil
}

// encodeTime stores timestamps in UTC RFC3339Nano form.
func encodeTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseStoredTime decodes a required RFC3339Nano timestamp.
func parseStoredTime(field string, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode %s: %w", field, err)
	}
	return t, nil
}

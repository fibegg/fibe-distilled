package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// playgroundJSONFields carries encoded JSON columns for Playground writes.
type playgroundJSONFields struct {
	Env           string
	Branches      string
	Services      string
	URLs          string
	Builds        string
	Steps         string
	StateReasons  string
	BuildWarnings string
	ErrorDetails  string
}

// encodePlaygroundJSON serializes every Playground JSON column fail-closed.
func encodePlaygroundJSON(p domain.Playground) (playgroundJSONFields, error) {
	var out playgroundJSONFields
	columns := []struct {
		target   *string
		field    string
		value    any
		fallback string
	}{
		{&out.Env, "playgrounds.env_overrides_json", p.EnvOverrides, "{}"},
		{&out.Branches, "playgrounds.service_branches_json", p.ServiceBranches, "{}"},
		{&out.Services, "playgrounds.services_json", p.Services, "[]"},
		{&out.URLs, "playgrounds.service_urls_json", p.ServiceURLs, "[]"},
		{&out.Builds, "playgrounds.build_statuses_json", p.BuildStatuses, "[]"},
		{&out.Steps, "playgrounds.creation_steps_json", p.CreationSteps, "[]"},
		{&out.StateReasons, "playgrounds.state_reasons_json", p.StateReasons, "[]"},
		{&out.BuildWarnings, "playgrounds.build_warnings_json", p.BuildWarnings, "[]"},
		{&out.ErrorDetails, "playgrounds.error_details_json", p.ErrorDetails, "{}"},
	}
	for _, column := range columns {
		encoded, err := encodeStoredJSON(column.field, column.value, column.fallback)
		if err != nil {
			return out, err
		}
		*column.target = encoded
	}
	return out, nil
}

// encodeStoredJSON marshals a value and replaces JSON null with a default.
func encodeStoredJSON(field string, value any, fallback string) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", field, err)
	}
	if string(raw) == "null" {
		return fallback, nil
	}
	return string(raw), nil
}

// storedJSONIsNull reports whether a stored JSON column contains top-level null.
func storedJSONIsNull(raw string) bool {
	return strings.TrimSpace(raw) == "null"
}

// encodeAsyncError serializes an optional async API error.
func encodeAsyncError(apiErr *domain.APIError) (sql.NullString, error) {
	if apiErr == nil {
		return sql.NullString{}, nil
	}
	raw, err := encodeStoredJSON("async_operations.error_json", apiErr, "{}")
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: raw, Valid: true}, nil
}

// decodeStoredJSON decodes one persisted JSON column with field context.
func decodeStoredJSON(raw string, field string, dst any) error {
	if storedJSONIsNull(raw) {
		return fmt.Errorf("decode %s: stored JSON null is not canonical", field)
	}
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return fmt.Errorf("decode %s: %w", field, err)
	}
	return nil
}

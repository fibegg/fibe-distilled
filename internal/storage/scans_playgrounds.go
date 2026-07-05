package storage

import (
	"database/sql"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// playgroundSelectSQL returns the shared Playground SELECT with joined names.
func playgroundSelectSQL() string {
	return `SELECT pg.id,pg.name,pg.status,pg.playspec_id,ps.name,pg.marquee_id,m.name,pg.compose_project,pg.root_domain,pg.routing_scheme,pg.internal_password,pg.env_overrides_json,pg.service_branches_json,pg.generated_compose_yaml,pg.services_json,pg.service_urls_json,pg.build_statuses_json,pg.creation_steps_json,pg.expires_at,pg.last_applied_at,pg.error_message,pg.state_reason,pg.state_reasons_json,pg.build_warnings_json,pg.error_details_json,pg.playguard_repair_reason,pg.playguard_repair_lock_until,pg.needs_recreation,pg.created_at,pg.updated_at FROM playgrounds pg LEFT JOIN playspecs ps ON ps.id=pg.playspec_id LEFT JOIN marquees m ON m.id=pg.marquee_id`
}

// playgroundScanValues groups nullable Playground columns during row decoding.
type playgroundScanValues struct {
	playspecID        sql.NullInt64
	playspecName      sql.NullString
	marqueeID         sql.NullInt64
	marqueeName       sql.NullString
	composeProject    sql.NullString
	rootDomain        sql.NullString
	scheme            sql.NullString
	password          sql.NullString
	envJSON           string
	branchesJSON      string
	servicesJSON      string
	urlsJSON          string
	buildsJSON        string
	stepsJSON         string
	expires           sql.NullString
	applied           sql.NullString
	errMsg            sql.NullString
	stateReason       sql.NullString
	stateReasonsJSON  string
	buildWarningsJSON string
	errorDetailsJSON  string
	repairReason      sql.NullString
	repairUntil       sql.NullString
	needsRecreation   int
	created           string
	updated           string
}

// scanPlayground decodes one Playground row and derived expiration metadata.
func scanPlayground(row scanner) (domain.Playground, error) {
	var p domain.Playground
	var values playgroundScanValues
	if err := row.Scan(
		&p.ID, &p.Name, &p.Status,
		&values.playspecID, &values.playspecName,
		&values.marqueeID, &values.marqueeName,
		&values.composeProject, &values.rootDomain, &values.scheme, &values.password,
		&values.envJSON, &values.branchesJSON, &p.GeneratedComposeYAML,
		&values.servicesJSON, &values.urlsJSON, &values.buildsJSON, &values.stepsJSON,
		&values.expires, &values.applied, &values.errMsg, &values.stateReason,
		&values.stateReasonsJSON, &values.buildWarningsJSON, &values.errorDetailsJSON,
		&values.repairReason, &values.repairUntil, &values.needsRecreation,
		&values.created, &values.updated,
	); err != nil {
		return p, err
	}
	if err := applyPlaygroundScalars(&p, values); err != nil {
		return p, err
	}
	if err := decodePlaygroundJSON(&p, values); err != nil {
		return p, err
	}
	decoratePlaygroundExpiration(&p)
	return p, nil
}

// applyPlaygroundScalars decodes non-JSON Playground columns.
func applyPlaygroundScalars(p *domain.Playground, values playgroundScanValues) error {
	var err error
	p.PlayspecID = int64Ptr(values.playspecID)
	p.PlayspecName = stringPtr(values.playspecName)
	p.MarqueeID = int64Ptr(values.marqueeID)
	p.MarqueeName = stringPtr(values.marqueeName)
	p.ComposeProject = stringPtr(values.composeProject)
	p.RootDomain = stringPtr(values.rootDomain)
	p.RoutingScheme = stringPtr(values.scheme)
	p.InternalPassword = stringPtr(values.password)
	if err := assignNullableStoredTime("playgrounds.expires_at", values.expires, &p.ExpiresAt); err != nil {
		return err
	}
	if err := assignNullableStoredTime("playgrounds.last_applied_at", values.applied, &p.LastAppliedAt); err != nil {
		return err
	}
	p.ErrorMessage = stringPtr(values.errMsg)
	p.StateReason = stringPtr(values.stateReason)
	p.PlayguardRepairReason = stringPtr(values.repairReason)
	if err := assignNullableStoredTime("playgrounds.playguard_repair_lock_until", values.repairUntil, &p.PlayguardRepairLockUntil); err != nil {
		return err
	}
	if values.needsRecreation == 1 {
		p.NeedsRecreation = new(true)
	}
	if p.CreatedAt, err = parseStoredTime("playgrounds.created_at", values.created); err != nil {
		return err
	}
	if p.UpdatedAt, err = parseStoredTime("playgrounds.updated_at", values.updated); err != nil {
		return err
	}
	return nil
}

// decodePlaygroundJSON decodes every Playground JSON column fail-closed.
func decodePlaygroundJSON(p *domain.Playground, values playgroundScanValues) error {
	if p.ServiceBranches == nil {
		p.ServiceBranches = map[string]any{}
	}
	if p.EnvOverrides == nil {
		p.EnvOverrides = map[string]string{}
	}
	jsonFields := []storedJSONField{
		{name: "playgrounds.env_overrides_json", raw: values.envJSON, dst: &p.EnvOverrides},
		{name: "playgrounds.service_branches_json", raw: values.branchesJSON, dst: &p.ServiceBranches},
		{name: "playgrounds.services_json", raw: values.servicesJSON, dst: &p.Services},
		{name: "playgrounds.service_urls_json", raw: values.urlsJSON, dst: &p.ServiceURLs},
		{name: "playgrounds.build_statuses_json", raw: values.buildsJSON, dst: &p.BuildStatuses},
		{name: "playgrounds.creation_steps_json", raw: values.stepsJSON, dst: &p.CreationSteps},
		{name: "playgrounds.state_reasons_json", raw: values.stateReasonsJSON, dst: &p.StateReasons},
		{name: "playgrounds.build_warnings_json", raw: values.buildWarningsJSON, dst: &p.BuildWarnings},
		{name: "playgrounds.error_details_json", raw: values.errorDetailsJSON, dst: &p.ErrorDetails},
	}
	return decodeStoredJSONFields(jsonFields)
}

// storedJSONField describes one JSON column decode target.
type storedJSONField struct {
	name string
	raw  string
	dst  any
}

// decodeStoredJSONFields decodes a group of JSON columns in order.
func decodeStoredJSONFields(fields []storedJSONField) error {
	for _, field := range fields {
		if err := decodeStoredJSON(field.raw, field.name, field.dst); err != nil {
			return err
		}
	}
	return nil
}

// decoratePlaygroundExpiration computes SDK-compatible TTL display fields.
func decoratePlaygroundExpiration(p *domain.Playground) {
	p.TimeRemaining = nil
	p.ExpirationPercentage = nil
	if p.ExpiresAt == nil {
		return
	}
	now := time.Now().UTC()
	remaining := nonNegativeSeconds(p.ExpiresAt.Sub(now))
	p.TimeRemaining = &remaining
	pct := expirationPercentage(p.CreatedAt, *p.ExpiresAt, now)
	p.ExpirationPercentage = &pct
}

// expirationPercentage computes elapsed lifetime percentage clamped to 0..100.
func expirationPercentage(createdAt time.Time, expiresAt time.Time, now time.Time) float64 {
	total := expiresAt.Sub(createdAt).Seconds()
	if total <= 0 || !now.Before(expiresAt) {
		return 100
	}
	elapsed := nonNegativeSeconds(now.Sub(createdAt))
	return clampPercentage((elapsed / total) * 100)
}

// nonNegativeSeconds converts a duration to seconds with a zero floor.
func nonNegativeSeconds(duration time.Duration) float64 {
	seconds := duration.Seconds()
	if seconds < 0 {
		return 0
	}
	return seconds
}

// clampPercentage keeps display percentages in the API's 0..100 range.
func clampPercentage(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

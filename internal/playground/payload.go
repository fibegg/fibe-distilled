package playground

import (
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/api/request"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

// playgroundPayload is the SDK Playground create/update payload.
type playgroundPayload struct {
	Name         string            `json:"name"`
	PlayspecID   *int64            `json:"-"`
	MarqueeID    *int64            `json:"-"`
	ExpiresAt    any               `json:"expires_at"`
	NeverExpire  *bool             `json:"never_expire"`
	EnvOverrides map[string]string `json:"env_overrides"`
	Services     map[string]any    `json:"services"`

	playspecIdentifier  string
	marqueeIdentifier   string
	blankPlayspecID     bool
	blankMarqueeID      bool
	expiresAtSet        bool
	invalidEnvOverrides string
	fields              jsonFields
}

// UnmarshalJSON records Playground field presence and name-or-ID references.
func (p *playgroundPayload) UnmarshalJSON(data []byte) error {
	type rawPlaygroundPayload playgroundPayload
	var raw struct {
		rawPlaygroundPayload
		PlayspecID   json.RawMessage `json:"playspec_id"`
		MarqueeID    json.RawMessage `json:"marquee_id"`
		EnvOverrides json.RawMessage `json:"env_overrides"`
	}
	fields, err := decodePayloadFields(data, &raw)
	if err != nil {
		return err
	}
	*p = playgroundPayload(raw.rawPlaygroundPayload)
	p.fields = fields
	p.expiresAtSet = fields.Has("expires_at")
	if fields.Has("env_overrides") {
		decoded := request.DecodeJSONStringScalarMap(raw.EnvOverrides, "env_overrides")
		p.EnvOverrides = decoded.Values
		p.invalidEnvOverrides = decoded.Invalid
	}
	p.blankPlayspecID = blankJSONReference(raw.PlayspecID)
	p.blankMarqueeID = blankJSONReference(raw.MarqueeID)
	playspecRef, err := parseIDReference(raw.PlayspecID, "playspec_id")
	if err != nil {
		return err
	}
	p.PlayspecID = playspecRef.id
	p.playspecIdentifier = playspecRef.identifier
	marqueeRef, err := parseIDReference(raw.MarqueeID, "marquee_id")
	if err != nil {
		return err
	}
	p.MarqueeID = marqueeRef.id
	p.marqueeIdentifier = marqueeRef.identifier
	return nil
}

// hasUpdateFields reports whether a Playground PATCH contains mutations.
func (p playgroundPayload) hasUpdateFields() bool {
	if p.fields.HasAny("name", "playspec_id", "marquee_id", "never_expire", "env_overrides") {
		return true
	}
	if p.fields.Has("services") && len(p.Services) > 0 {
		return true
	}
	return p.expiresAtSet && p.ExpiresAt != nil
}

// mutatesRuntimeConfig reports whether payload changes deployed runtime intent.
func (p playgroundPayload) mutatesRuntimeConfig() bool {
	return p.PlayspecID != nil || p.MarqueeID != nil || p.EnvOverrides != nil || len(p.Services) > 0
}

// toDomain converts a create payload into a Playground row.
func (p playgroundPayload) toDomain() domain.Playground {
	services := copyAnyMap(p.Services)
	return domain.Playground{
		Name:            normalizeScalarInput(p.Name),
		PlayspecID:      p.PlayspecID,
		MarqueeID:       p.MarqueeID,
		ExpiresAt:       p.expiration(),
		EnvOverrides:    p.EnvOverrides,
		ServiceBranches: services,
	}
}

// apply mutates a Playground row with PATCH fields.
func (p playgroundPayload) apply(pg *domain.Playground) {
	if p.Name != "" {
		pg.Name = normalizeScalarInput(p.Name)
	}
	if p.PlayspecID != nil {
		pg.PlayspecID = p.PlayspecID
	}
	if p.MarqueeID != nil {
		pg.MarqueeID = p.MarqueeID
	}
	if p.EnvOverrides != nil {
		pg.EnvOverrides = p.EnvOverrides
	}
	if p.Services != nil {
		pg.ServiceBranches = mergeAnyMaps(pg.ServiceBranches, p.Services)
	}
	p.applyExpiration(pg, time.Now().UTC())
}

// expiration parses the payload expires_at value.
func (p playgroundPayload) expiration() *time.Time {
	expiresAt, _ := parsePayloadTime(p.ExpiresAt)
	return expiresAt
}

// applyExpiration applies never_expire and expires_at semantics.
func (p playgroundPayload) applyExpiration(pg *domain.Playground, now time.Time) {
	if p.NeverExpire != nil {
		if *p.NeverExpire {
			pg.ExpiresAt = nil
			return
		}
		if expiresAt := p.expiration(); expiresAt != nil {
			pg.ExpiresAt = expiresAt
			return
		}
		expires := now.Add(defaultPlaygroundTTL())
		pg.ExpiresAt = &expires
		return
	}
	if expiresAt := p.expiration(); expiresAt != nil {
		pg.ExpiresAt = expiresAt
	}
}

// playgroundOperationPayload is the Playground action request shape.
type playgroundOperationPayload struct {
	ActionType string         `json:"action_type"`
	Force      *bool          `json:"force"`
	Playground map[string]any `json:"playground"`

	fields jsonFields
}

// UnmarshalJSON records Playground operation field presence.
func (p *playgroundOperationPayload) UnmarshalJSON(data []byte) error {
	type rawPlaygroundOperationPayload playgroundOperationPayload
	return decodePayloadInto(data, (*rawPlaygroundOperationPayload)(p), &p.fields)
}

// force unwraps the optional operation force flag.
func (p playgroundOperationPayload) force() bool {
	return p.Force != nil && *p.Force
}

// playgroundLogsPayload is the Playground logs request shape.
type playgroundLogsPayload struct {
	Service *string `json:"service"`
	Tail    *int    `json:"tail"`

	fields jsonFields
}

// UnmarshalJSON records Playground logs field presence.
func (p *playgroundLogsPayload) UnmarshalJSON(data []byte) error {
	type rawPlaygroundLogsPayload playgroundLogsPayload
	return decodePayloadInto(data, (*rawPlaygroundLogsPayload)(p), &p.fields)
}

// service returns the trimmed requested log service.
func (p playgroundLogsPayload) service() string {
	if p.Service == nil {
		return ""
	}
	return strings.TrimSpace(*p.Service)
}

// playgroundExpirationPayload is the expiration-extension request shape.
type playgroundExpirationPayload struct {
	DurationHours *int `json:"duration_hours"`

	fields jsonFields
}

// UnmarshalJSON records Playground expiration field presence.
func (p *playgroundExpirationPayload) UnmarshalJSON(data []byte) error {
	type rawPlaygroundExpirationPayload playgroundExpirationPayload
	return decodePayloadInto(data, (*rawPlaygroundExpirationPayload)(p), &p.fields)
}

// validatePlaygroundPayload validates shared Playground payload fields.
func validatePlaygroundPayload(payload playgroundPayload) error {
	if err := validatePlaygroundScalarFields(payload); err != nil {
		return err
	}
	if err := request.ValidateNonEmptyObjectMapField(payload.fields.Has("services"), payload.Services, "services"); err != nil {
		return badRequestError{message: err.Error()}
	}
	if err := validatePlaygroundReferenceFields(payload); err != nil {
		return err
	}
	return validatePlaygroundExpiration(payload)
}

// validatePlaygroundScalarFields validates scalar Playground payload fields.
func validatePlaygroundScalarFields(payload playgroundPayload) error {
	if presentBlankString(payload.fields, "name", payload.Name) {
		return badRequestError{message: "name must not be blank"}
	}
	if payload.fields.Has("never_expire") && payload.NeverExpire == nil {
		return badRequestError{message: "never_expire must be true or false"}
	}
	if payload.invalidEnvOverrides != "" {
		return badRequestError{message: payload.invalidEnvOverrides}
	}
	if err := request.ValidateStringMapField(payload.fields.Has("env_overrides"), payload.EnvOverrides, "env_overrides", true); err != nil {
		return badRequestError{message: err.Error()}
	}
	return nil
}

// validatePlaygroundReferenceFields validates name-or-ID references.
func validatePlaygroundReferenceFields(payload playgroundPayload) error {
	if payload.blankPlayspecID {
		return badRequestError{message: "playspec_id must not be blank"}
	}
	if payload.blankMarqueeID {
		return badRequestError{message: "marquee_id must not be blank"}
	}
	return nil
}

// validatePlaygroundExpiration validates expires_at when present.
func validatePlaygroundExpiration(payload playgroundPayload) error {
	if !payload.expiresAtSet {
		return nil
	}
	if payload.ExpiresAt == nil {
		return badRequestError{message: "expires_at must be an RFC3339 timestamp or integer Unix timestamp"}
	}
	if _, ok := parsePayloadTime(payload.ExpiresAt); ok {
		return nil
	}
	return badRequestError{message: "expires_at must be an RFC3339 timestamp or integer Unix timestamp"}
}

// validatePlaygroundCreatePayload validates create-only Playground fields.
func validatePlaygroundCreatePayload(payload playgroundPayload) error {
	if payload.fields.Has("env_overrides") && len(payload.EnvOverrides) == 0 {
		return badRequestError{message: "env_overrides must not be empty"}
	}
	return nil
}

// copyAnyMap shallow-copies override maps.
func copyAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

// mergeAnyMaps recursively merges service override maps.
func mergeAnyMaps(base map[string]any, incoming map[string]any) map[string]any {
	out := copyAnyMap(base)
	for key, value := range incoming {
		if current, ok := out[key].(map[string]any); ok {
			if next, ok := value.(map[string]any); ok {
				out[key] = mergeAnyMaps(current, next)
				continue
			}
		}
		out[key] = value
	}
	return out
}

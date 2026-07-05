package launch

import (
	"encoding/json"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/request"
)

// launchPayload is the SDK launch request shape.
type launchPayload struct {
	ComposeYAML       string            `json:"compose_yaml"`
	Name              string            `json:"name"`
	RepositoryURL     string            `json:"repository_url"`
	MarqueeID         *int64            `json:"-"`
	CreatePlayground  *bool             `json:"create_playground"`
	PersistVolumes    *bool             `json:"persist_volumes"`
	EnvOverrides      map[string]string `json:"env_overrides"`
	Variables         map[string]string `json:"variables"`
	ServiceSubdomains map[string]string `json:"service_subdomains"`
	Services          map[string]any    `json:"services"`

	marqueeIdentifier      string
	blankMarqueeID         bool
	nullServiceSubdomains  bool
	blankServiceSubdomains bool
	ambiguousSubdomains    bool
	invalidEnvOverrides    string
	invalidVariables       string
	fields                 jsonFields
}

// UnmarshalJSON records Launch field presence and name-or-ID Marquee references.
func (p *launchPayload) UnmarshalJSON(data []byte) error {
	type rawLaunchPayload launchPayload
	var raw struct {
		rawLaunchPayload
		MarqueeID    json.RawMessage `json:"marquee_id"`
		EnvOverrides json.RawMessage `json:"env_overrides"`
		Variables    json.RawMessage `json:"variables"`
	}
	fields, err := request.DecodePayloadFields(data, &raw)
	if err != nil {
		return err
	}
	*p = launchPayload(raw.rawLaunchPayload)
	p.fields = fields
	if fields.Has("env_overrides") {
		decoded := request.DecodeJSONStringScalarMap(raw.EnvOverrides, "env_overrides")
		p.EnvOverrides = decoded.Values
		p.invalidEnvOverrides = decoded.Invalid
	}
	if fields.Has("variables") {
		decoded := request.DecodeJSONStringScalarMap(raw.Variables, "variables")
		p.Variables = decoded.Values
		p.invalidVariables = decoded.Invalid
	}
	p.blankMarqueeID = blankJSONReference(raw.MarqueeID)
	p.nullServiceSubdomains = fields.Has("service_subdomains") && raw.ServiceSubdomains == nil
	p.blankServiceSubdomains = hasBlankServiceSubdomains(raw.ServiceSubdomains)
	p.ambiguousSubdomains = hasTrimmedStringMapKeyCollision(raw.ServiceSubdomains)
	marqueeRef, err := parseIDReference(raw.MarqueeID, "marquee_id")
	if err != nil {
		return err
	}
	p.MarqueeID = marqueeRef.id
	p.marqueeIdentifier = marqueeRef.identifier
	p.ServiceSubdomains = normalizedServiceSubdomains(p.ServiceSubdomains)
	return nil
}

// normalizedServiceSubdomains trims explicit service-name subdomain overrides.
func normalizedServiceSubdomains(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for service, value := range values {
		service = strings.TrimSpace(service)
		value = strings.TrimSpace(value)
		if service == "" || value == "" {
			continue
		}
		out[service] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// nameFromRepo derives a default resource name from a repository URL.
func nameFromRepo(repo string) string {
	repo = strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(repo), "/"), ".git")
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return ""
	}
	return parts[len(parts)-1]
}

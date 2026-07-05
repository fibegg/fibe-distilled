package prop

import "github.com/fibegg/fibe-distilled/internal/domain"

// propPayload is the SDK Prop create/update payload.
type propPayload struct {
	RepositoryURL string         `json:"repository_url"`
	Name          *string        `json:"name"`
	Private       *bool          `json:"private"`
	DefaultBranch *string        `json:"default_branch"`
	Provider      *string        `json:"provider"`
	Credentials   map[string]any `json:"credentials"`

	fields jsonFields
}

// UnmarshalJSON records Prop payload field presence for strict API validation.
func (p *propPayload) UnmarshalJSON(data []byte) error {
	type rawPropPayload propPayload
	return decodePayloadInto(data, (*rawPropPayload)(p), &p.fields)
}

// hasUpdateFields reports whether a Prop PATCH contains mutations.
func (p propPayload) hasUpdateFields() bool {
	return p.fields.HasAny("repository_url", "name", "private", "default_branch", "provider")
}

// toDomain converts a Prop create payload into a row.
func (p propPayload) toDomain() domain.Prop {
	out := domain.Prop{RepositoryURL: normalizeRepositoryURLInput(p.RepositoryURL)}
	if p.Name != nil {
		out.Name = normalizeScalarInput(*p.Name)
	}
	if p.Private != nil {
		out.Private = *p.Private
	}
	if p.DefaultBranch != nil {
		out.DefaultBranch = normalizeScalarInput(*p.DefaultBranch)
	}
	if p.Provider != nil {
		out.Provider = normalizePropProvider(*p.Provider)
	}
	return out
}

// apply mutates a Prop row from a PATCH payload.
func (p propPayload) apply(prop *domain.Prop) {
	if p.RepositoryURL != "" {
		prop.RepositoryURL = normalizeRepositoryURLInput(p.RepositoryURL)
	}
	if p.Name != nil {
		prop.Name = normalizeScalarInput(*p.Name)
	}
	if p.Private != nil {
		prop.Private = *p.Private
	}
	if p.DefaultBranch != nil {
		prop.DefaultBranch = normalizeScalarInput(*p.DefaultBranch)
	}
	if p.Provider != nil {
		prop.Provider = normalizePropProvider(*p.Provider)
	}
}

// repoStatusPayload is the repository-status request shape.
type repoStatusPayload struct {
	GitHubURLs []string `json:"github_urls"`

	fields jsonFields
}

// UnmarshalJSON records repository-status payload field presence.
func (p *repoStatusPayload) UnmarshalJSON(data []byte) error {
	type rawRepoStatusPayload repoStatusPayload
	return decodePayloadInto(data, (*rawRepoStatusPayload)(p), &p.fields)
}

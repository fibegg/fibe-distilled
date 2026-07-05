package playspec

import (
	"errors"
	"net/http"
	"strings"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

// payload is the SDK Playspec create/update payload.
type payload struct {
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	BaseComposeYAML string  `json:"base_compose_yaml"`
	PersistVolumes  *bool   `json:"persist_volumes"`
	Services        []any   `json:"services"`

	fields jsonFields
}

// UnmarshalJSON records Playspec payload field presence.
func (p *payload) UnmarshalJSON(data []byte) error {
	type rawPayload payload
	return decodePayloadInto(data, (*rawPayload)(p), &p.fields)
}

// hasUpdateFields reports whether a Playspec PATCH contains mutations.
func (p payload) hasUpdateFields() bool {
	return p.fields.HasAny("name", "description", "base_compose_yaml", "persist_volumes", "services")
}

// toDomain validates and converts a create payload into a Playspec row.
func (p payload) toDomain() (domain.Playspec, error) {
	if err := rejectPersistVolumes(p.PersistVolumes); err != nil {
		return domain.Playspec{}, err
	}
	composeYAML, err := ApplyPlayspecServices(p.BaseComposeYAML, p.Services)
	if err != nil {
		return domain.Playspec{}, err
	}
	validation := compose.Validate(composeYAML)
	if !validation.Valid {
		return domain.Playspec{}, errors.New(strings.Join(validation.Errors, "; "))
	}
	return domain.Playspec{
		Name:            normalizeScalarInput(p.Name),
		Description:     p.Description,
		BaseComposeYAML: composeYAML,
		PersistVolumes:  p.PersistVolumes,
		Services:        servicesForValidation(validation, p.Services),
	}, nil
}

// apply mutates a Playspec row from a PATCH payload.
func (p payload) apply(ps *domain.Playspec) error {
	if err := rejectPersistVolumes(p.PersistVolumes); err != nil {
		return err
	}
	if p.Name != "" {
		ps.Name = normalizeScalarInput(p.Name)
	}
	if p.fields.Has("description") {
		ps.Description = p.Description
	}
	if baseComposeYAML, ok := p.composeYAMLForUpdate(ps.BaseComposeYAML); ok {
		if err := p.applyCompose(ps, baseComposeYAML); err != nil {
			return err
		}
	}
	if p.PersistVolumes != nil {
		ps.PersistVolumes = p.PersistVolumes
	}
	return nil
}

// composeYAMLForUpdate selects the Compose source affected by a PATCH payload.
func (p payload) composeYAMLForUpdate(current string) (string, bool) {
	if p.BaseComposeYAML != "" {
		return p.BaseComposeYAML, true
	}
	if p.Services != nil {
		return current, true
	}
	return "", false
}

// applyCompose applies service metadata and validates Compose YAML.
func (p payload) applyCompose(ps *domain.Playspec, baseComposeYAML string) error {
	composeYAML, err := ApplyPlayspecServices(baseComposeYAML, p.Services)
	if err != nil {
		return err
	}
	validation := compose.Validate(composeYAML)
	if !validation.Valid {
		return errors.New(strings.Join(validation.Errors, "; "))
	}
	ps.BaseComposeYAML = composeYAML
	ps.Services = servicesForValidation(validation, p.Services)
	return nil
}

// validatePayload validates Playspec request fields.
func validatePayload(payload payload) error {
	if presentBlankString(payload.fields, "name", payload.Name) {
		return badRequestError{message: "name must not be blank"}
	}
	if presentBlankString(payload.fields, "base_compose_yaml", payload.BaseComposeYAML) {
		return badRequestError{message: "base_compose_yaml must not be blank"}
	}
	if payload.fields.Has("persist_volumes") && payload.PersistVolumes == nil {
		return badRequestError{message: "persist_volumes must be true or false"}
	}
	if payload.fields.Has("services") && payload.Services == nil {
		return badRequestError{message: "services must be an array"}
	}
	return nil
}

// rejectPersistVolumes enforces fibe-distilled's stateless runtime scope.
func rejectPersistVolumes(value *bool) error {
	if value == nil || !*value {
		return nil
	}
	return apiValidationError{
		status:  http.StatusNotImplemented,
		code:    "NOT_IMPLEMENTED",
		message: "fibe-distilled Playgrounds and Playspecs are stateless; persist_volumes=true is not implemented",
		details: map[string]any{"unsupported": []string{"field:persist_volumes"}},
	}
}

// serviceFromSummary builds SDK service metadata from validation output.
func serviceFromSummary(summary service.Summary) map[string]any {
	service := map[string]any{
		"name":  summary.Name,
		"type":  summary.Type,
		"image": summary.Image,
		"exposure": map[string]any{
			"enabled":    summary.Port > 0,
			"port":       summary.Port,
			"subdomain":  summary.Subdomain,
			"visibility": summary.Visibility,
		},
	}
	if summary.Type == "dynamic" {
		addDynamicServiceFields(service, summary)
	}
	return service
}

// addDynamicServiceFields adds SDK metadata for source-backed services.
func addDynamicServiceFields(service map[string]any, summary service.Summary) {
	service["repo_url"] = summary.RepoURL
	if summary.Production {
		service["production"] = true
	}
	addNonEmptyMapValue(service, "dockerfile_path", summary.Dockerfile)
	addNonEmptyMapValue(service, "start_command", summary.StartCommand)
	addNonEmptyMapValue(service, "build_target", summary.BuildTarget)
	addNonEmptyMapValue(service, "build_args", summary.BuildArgs)
}

// servicesForValidation preserves provided metadata or derives it.
func servicesForValidation(validation compose.Validation, provided []any) []any {
	if len(provided) > 0 {
		return provided
	}
	services := make([]any, 0, len(validation.Services))
	for _, summary := range validation.Services {
		services = append(services, serviceFromSummary(summary))
	}
	return services
}

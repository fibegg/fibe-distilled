package composefile

import (
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	fibetemplate "github.com/fibegg/fibe-distilled/internal/composefile/template"
)

// Validation is the internal result of checking Compose syntax and Fibe labels.
type Validation struct {
	// Valid reports whether blocking errors were found.
	Valid bool `json:"valid"`
	// Services is the normalized service metadata parsed from Compose.
	Services []service.Summary `json:"services,omitempty"`
	// Errors are blocking validation failures.
	Errors []string `json:"errors,omitempty"`
}

// Validate checks Compose syntax, supported Fibe labels, and template declarations.
func Validate(composeYAML string) Validation {
	doc, err := parseDocument(composeYAML)
	if err != nil {
		return Validation{Valid: false, Errors: []string{err.Error()}}
	}
	summaries := service.Summaries(doc.Services)
	if len(summaries) == 0 {
		return Validation{Valid: false, Errors: []string{"compose must define at least one service"}}
	}
	errors := serviceLabelValidationErrors(doc, summaries)
	rendered, err := MutationMap(composeYAML)
	if err != nil {
		errors = append(errors, err.Error())
	} else {
		errors = append(errors, fibetemplate.ValidateDeclarations(rendered)...)
	}
	if len(errors) > 0 {
		return Validation{Valid: false, Services: summaries, Errors: errors}
	}
	return Validation{Valid: true, Services: summaries}
}

// serviceLabelValidationErrors checks all supported Fibe service labels.
func serviceLabelValidationErrors(doc *document, summaries []service.Summary) []string {
	errors := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		definition := doc.Services[summary.Name]
		errors = append(errors, service.ValidationErrors(summary.Name, definition)...)
	}
	return errors
}

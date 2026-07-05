package template

import (
	"errors"
	"sort"
	"strings"
)

// ValidateDeclarations checks Fibe template declarations and references without rendering values.
func ValidateDeclarations(rendered map[string]any) []string {
	block := templateVariableBlockFrom(rendered)
	if block.invalidBlock {
		return []string{"x-fibe.gg.variables must be an object"}
	}
	if !block.needsCompilation(rendered, nil) {
		return nil
	}
	errs := validateTemplateVariableDefinitions(block.definitions)
	if err := validateTemplateVariableReferences(rendered, block.definitions); err != nil {
		errs = append(errs, err.Error())
	}
	sort.Strings(errs)
	return errs
}

// validateTemplateVariableDefinitions checks each declared variable's schema.
func validateTemplateVariableDefinitions(definitions map[string]any) []string {
	errs := make([]string, 0, len(definitions))
	for key, raw := range definitions {
		errs = append(errs, validateTemplateVariableDeclaration(key, raw)...)
	}
	return errs
}

// validateTemplateVariableDeclaration checks one declaration without requiring a value.
func validateTemplateVariableDeclaration(key string, raw any) []string {
	name := key
	if !templateVariableNamePattern.MatchString(name) {
		return []string{`Variable "` + key + `" has invalid name`}
	}
	definition, ok := asMap(raw)
	if !ok {
		return []string{`Variable "` + name + `" definition must be an object`}
	}
	return validateTemplateVariableDeclarationFields(name, definition)
}

// validateTemplateVariableDeclarationFields checks non-value declaration fields.
func validateTemplateVariableDeclarationFields(name string, definition map[string]any) []string {
	errs := validateTemplateVariableDefinitionFields(name, definition)
	if err := validateTemplateDefault(name, definition); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateTemplateVariablePatternDeclaration(name, definition); err != nil {
		errs = append(errs, err.Error())
	}
	return errs
}

// joinTemplateDeclarationErrors converts declaration messages to an error.
func joinTemplateDeclarationErrors(messages []string) error {
	if len(messages) == 0 {
		return nil
	}
	sort.Strings(messages)
	return errors.New(strings.Join(messages, "; "))
}

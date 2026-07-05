package template

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// compiledTemplateVariables separates inline replacements from path values.
type compiledTemplateVariables struct {
	inline map[string]string
	path   map[string]any
}

// compiledTemplateVariableDefinition is one validated template variable value.
type compiledTemplateVariableDefinition struct {
	name     string
	value    any
	hasValue bool
}

// templateVariableNamePattern matches Fibe variable names after $$var__.
var templateVariableNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// templatePlaceholderValue matches FibeCore's missing inline variable fallback.
const templatePlaceholderValue = "placeholder"

// compileTemplateVariableValues validates definitions and supplied values.
func compileTemplateVariableValues(definitions map[string]any, provided map[string]string, readRandomBytes func([]byte) (int, error)) (compiledTemplateVariables, error) {
	out := compiledTemplateVariables{
		inline: map[string]string{},
		path:   map[string]any{},
	}
	var errs []string
	for key, raw := range definitions {
		result := compileTemplateVariableDefinition(key, raw, provided, readRandomBytes)
		errs = append(errs, result.errs...)
		if !result.ok {
			continue
		}
		out.inline[result.definition.name] = inlineTemplateValue(result.definition)
		out.path[result.definition.name] = result.definition.value
	}
	errs = append(errs, undeclaredTemplateInputs(definitions, provided)...)
	if len(errs) > 0 {
		sort.Strings(errs)
		return out, errors.New(strings.Join(errs, "; "))
	}
	return out, nil
}

// compiledTemplateVariableDefinitionResult carries one compiled definition.
type compiledTemplateVariableDefinitionResult struct {
	definition compiledTemplateVariableDefinition
	errs       []string
	ok         bool
}

// compileTemplateVariableDefinition validates one x-fibe.gg variable definition.
func compileTemplateVariableDefinition(key string, raw any, provided map[string]string, readRandomBytes func([]byte) (int, error)) compiledTemplateVariableDefinitionResult {
	name := key
	if !templateVariableNamePattern.MatchString(name) {
		return compiledTemplateVariableDefinitionResult{errs: []string{fmt.Sprintf("Variable %q has invalid name", key)}}
	}
	definition, ok := asMap(raw)
	if !ok {
		return compiledTemplateVariableDefinitionResult{errs: []string{fmt.Sprintf("Variable %q definition must be an object", name)}}
	}
	value := compileTemplateVariableDefinitionValue(name, definition, provided, readRandomBytes)
	return compiledTemplateVariableDefinitionResult{
		definition: compiledTemplateVariableDefinition{name: name, value: value.value, hasValue: value.hasValue},
		errs:       value.errs,
		ok:         true,
	}
}

// templateVariableDefinitionValue carries one resolved variable value.
type templateVariableDefinitionValue struct {
	value    any
	hasValue bool
	errs     []string
}

// compileTemplateVariableDefinitionValue resolves provided, default, or random value.
func compileTemplateVariableDefinitionValue(name string, definition map[string]any, provided map[string]string, readRandomBytes func([]byte) (int, error)) templateVariableDefinitionValue {
	errs := validateTemplateVariableDefinitionFields(name, definition)
	value := templateVariableValue(name, definition, provided, readRandomBytes)
	if value.err != nil {
		errs = append(errs, value.err.Error())
	}
	if err := validateTemplateDefault(name, definition); err != nil {
		errs = append(errs, err.Error())
	}
	if required, _ := definition["required"].(bool); required && !value.hasValue {
		errs = append(errs, fmt.Sprintf("Variable %q is required", name))
	}
	if err := validateTemplateVariableValuePattern(name, definition, value.value); err != nil {
		errs = append(errs, err.Error())
	}
	return templateVariableDefinitionValue{value: value.value, hasValue: value.hasValue, errs: errs}
}

// validateTemplateVariableDefinitionFields checks Fibe template-variable field types.
func validateTemplateVariableDefinitionFields(name string, definition map[string]any) []string {
	errs := make([]string, 0, 6)
	errs = append(errs, validateTemplateVariableNameField(name, definition)...)
	errs = append(errs, validateTemplateBooleanField(name, definition, "required")...)
	errs = append(errs, validateTemplateBooleanField(name, definition, "random")...)
	errs = append(errs, validateTemplateDefaultField(name, definition)...)
	errs = append(errs, validateTemplateValidationField(name, definition)...)
	errs = append(errs, validateTemplatePathFields(name, definition)...)
	return errs
}

// validateTemplateVariableNameField requires the public display name string.
func validateTemplateVariableNameField(name string, definition map[string]any) []string {
	raw, ok := definition["name"]
	if !ok || raw == nil {
		return []string{fmt.Sprintf("Variable %q is missing name", name)}
	}
	displayName, ok := raw.(string)
	if !ok {
		return []string{fmt.Sprintf("Variable %q name must be a string", name)}
	}
	if strings.TrimSpace(displayName) == "" {
		return []string{fmt.Sprintf("Variable %q is missing name", name)}
	}
	return nil
}

// validateTemplateBooleanField requires boolean Fibe template flags.
func validateTemplateBooleanField(name string, definition map[string]any, field string) []string {
	raw, ok := definition[field]
	if !ok {
		return nil
	}
	if _, ok := raw.(bool); !ok {
		return []string{fmt.Sprintf("Variable %q %s must be a boolean", name, field)}
	}
	return nil
}

// validateTemplateDefaultField requires defaults to be Fibe literal scalars.
func validateTemplateDefaultField(name string, definition map[string]any) []string {
	raw, ok := definition["default"]
	if !ok || raw == nil {
		return nil
	}
	if isTemplateLiteralScalar(raw) {
		return nil
	}
	return []string{fmt.Sprintf("Variable %q default must be a string, number, boolean, or null", name)}
}

// validateTemplateValidationField requires validation to be a string when present.
func validateTemplateValidationField(name string, definition map[string]any) []string {
	raw, ok := definition["validation"]
	if !ok {
		return nil
	}
	if _, ok := raw.(string); !ok {
		return []string{fmt.Sprintf("Variable %q validation must be a string", name)}
	}
	return nil
}

// inlineTemplateValue returns the FibeCore fallback for missing inline variables.
func inlineTemplateValue(compiled compiledTemplateVariableDefinition) string {
	if !compiled.hasValue {
		return templatePlaceholderValue
	}
	return templateScalarText(compiled.value)
}

// validateTemplateVariableValuePattern applies an optional validation regex.
func validateTemplateVariableValuePattern(name string, definition map[string]any, value any) error {
	raw, ok := definition["validation"]
	if !ok {
		return nil
	}
	pattern, ok := raw.(string)
	if !ok || pattern == "" {
		return nil
	}
	return validateTemplateVariablePattern(name, pattern, templateScalarText(value))
}

// undeclaredTemplateInputs reports supplied variables without definitions.
func undeclaredTemplateInputs(definitions map[string]any, provided map[string]string) []string {
	var errs []string
	for key := range provided {
		if _, ok := definitions[key]; !ok {
			errs = append(errs, fmt.Sprintf("Variable %q is not declared", key))
		}
	}
	return errs
}

// templateVariableValueResult carries a resolved variable value or error.
type templateVariableValueResult struct {
	value    any
	hasValue bool
	err      error
}

// templateVariableValue resolves one variable's effective value.
func templateVariableValue(name string, definition map[string]any, provided map[string]string, readRandomBytes func([]byte) (int, error)) templateVariableValueResult {
	if value, ok := provided[name]; ok && value != "" {
		return templateVariableValueResult{value: coerceTemplateScalar(value), hasValue: true}
	}
	if value, ok := templateScalarString(definition["default"]); ok {
		return templateVariableValueResult{value: coerceTemplateScalar(value), hasValue: true}
	}
	if random, _ := definition["random"].(bool); random {
		value, err := RandomSecret(readRandomBytes)
		if err != nil {
			return templateVariableValueResult{err: fmt.Errorf("variable %q random value generation failed: %w", name, err)}
		}
		return templateVariableValueResult{value: value, hasValue: true}
	}
	return templateVariableValueResult{}
}

package template

import (
	"fmt"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
)

// templateValidationTimeout bounds user-supplied regex evaluation.
const templateValidationTimeout = 500 * time.Millisecond

// forbiddenTemplateDefaultTokens prevents defaults from expanding recursively.
var forbiddenTemplateDefaultTokens = []string{
	"$$var__",
	"$$random__",
	"$$root_domain",
}

// validateTemplateDefault requires literal variable defaults.
func validateTemplateDefault(name string, definition map[string]any) error {
	text, ok := templateScalarString(definition["default"])
	if !ok {
		return nil
	}
	for _, token := range forbiddenTemplateDefaultTokens {
		if strings.Contains(text, token) {
			return fmt.Errorf("variable %q default must be a literal", name)
		}
	}
	return nil
}

// validateTemplateVariablePatternFormat checks /.../ regex syntax.
func validateTemplateVariablePatternFormat(name string, pattern string) error {
	if !strings.HasPrefix(pattern, "/") || !strings.HasSuffix(pattern, "/") || len(pattern) < 2 {
		return fmt.Errorf("variable %q validation must be wrapped in /.../", name)
	}
	return nil
}

// validateTemplateVariablePatternDeclaration checks a regex without matching a value.
func validateTemplateVariablePatternDeclaration(name string, definition map[string]any) error {
	raw, ok := definition["validation"]
	if !ok {
		return nil
	}
	pattern, ok := raw.(string)
	if !ok || pattern == "" {
		return nil
	}
	_, err := compileTemplateVariablePattern(name, pattern)
	return err
}

// compileTemplateVariablePattern builds a bounded Ruby-style template regex.
func compileTemplateVariablePattern(name string, pattern string) (*regexp2.Regexp, error) {
	if err := validateTemplateVariablePatternFormat(name, pattern); err != nil {
		return nil, err
	}
	re, err := regexp2.Compile(strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/"), regexp2.None)
	if err != nil {
		return nil, fmt.Errorf("variable %q validation regex is invalid: %w", name, err)
	}
	re.MatchTimeout = templateValidationTimeout
	return re, nil
}

// validateTemplateVariablePattern validates a value against a Fibe regex.
func validateTemplateVariablePattern(name string, pattern string, value string) error {
	re, err := compileTemplateVariablePattern(name, pattern)
	if err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	matched, err := re.MatchString(value)
	if err != nil {
		return fmt.Errorf("variable %q validation regex evaluation failed: %w", name, err)
	}
	if !matched {
		return fmt.Errorf("variable %q fails validation pattern", name)
	}
	return nil
}

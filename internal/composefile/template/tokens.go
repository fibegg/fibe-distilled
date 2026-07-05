package template

import (
	"errors"
	"slices"
	"strings"
)

// ApplyRootDomainToken replaces $$root_domain after Marquee selection.
func ApplyRootDomainToken(rendered map[string]any, rootDomain string) error {
	rootDomain = strings.TrimSpace(rootDomain)
	if !containsRootDomainToken(rendered) {
		return nil
	}
	if rootDomain == "" {
		return errors.New("$$root_domain requires a selected Marquee with domains_input")
	}
	replaceRootDomainToken(rendered, rootDomain)
	return nil
}

// containsRootDomainToken walks rendered Compose looking for $$root_domain.
func containsRootDomainToken(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		return anyMapValue(typed, containsRootDomainToken)
	case []any:
		return anySliceValue(typed, containsRootDomainToken)
	case string:
		return strings.Contains(typed, "$$root_domain")
	}
	return false
}

// anyMapValue reports whether any map value satisfies a predicate.
func anyMapValue(values map[string]any, predicate func(any) bool) bool {
	for _, value := range values {
		if predicate(value) {
			return true
		}
	}
	return false
}

// anySliceValue reports whether any slice value satisfies a predicate.
func anySliceValue(values []any, predicate func(any) bool) bool {
	return slices.ContainsFunc(values, predicate)
}

// replaceRootDomainToken walks and replaces $$root_domain strings.
func replaceRootDomainToken(value any, rootDomain string) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = replaceRootDomainToken(item, rootDomain)
		}
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = replaceRootDomainToken(item, rootDomain)
		}
		return typed
	case string:
		return strings.ReplaceAll(typed, "$$root_domain", rootDomain)
	default:
		return value
	}
}

// replaceInlineTemplateVariables walks and replaces $$var__/$$random__ tokens.
func replaceInlineTemplateVariables(value any, variables map[string]string) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = replaceInlineTemplateVariables(item, variables)
		}
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = replaceInlineTemplateVariables(item, variables)
		}
		return typed
	case string:
		return replaceTemplateTokens(typed, variables)
	default:
		return value
	}
}

// replaceTemplateTokens substitutes declared inline Fibe template tokens.
func replaceTemplateTokens(value string, variables map[string]string) string {
	return templateVariableReferencePattern.ReplaceAllStringFunc(value, func(token string) string {
		if value, ok := templateTokenValue(token, variables); ok {
			return value
		}
		return token
	})
}

// templateTokenValue resolves one $$var__ or $$random__ reference.
func templateTokenValue(token string, variables map[string]string) (string, bool) {
	name := strings.TrimPrefix(strings.TrimPrefix(token, "$$var__"), "$$random__")
	value, ok := variables[name]
	return value, ok
}

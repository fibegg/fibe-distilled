package template

import (
	"regexp"
	"strings"
)

// templateVariableReferencePattern matches Fibe inline variable tokens.
var templateVariableReferencePattern = regexp.MustCompile(`\$\$(var|random)__([A-Za-z0-9_]+)`)

// ContainsVariable reports whether text includes a Fibe variable token.
func ContainsVariable(value string) bool {
	return containsTemplateVariable(value)
}

// containsTemplateVariable reports whether text includes a Fibe variable token.
func containsTemplateVariable(value string) bool {
	return strings.Contains(value, "$$var__") || strings.Contains(value, "$$random__")
}

// HasUnresolvedTokens reports whether rendered Compose still contains Fibe template tokens.
func HasUnresolvedTokens(value string) bool {
	return containsTemplateVariable(value) || strings.Contains(value, "$$root_domain")
}

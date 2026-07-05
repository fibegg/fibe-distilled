package service

import (
	"fmt"
	"strings"

	fibetemplate "github.com/fibegg/fibe-distilled/internal/composefile/template"
	"github.com/fibegg/fibe-distilled/internal/git"
)

// allowedPathRulePredicates are the Traefik predicates fibe-distilled lets callers compose with generated Host rules.
var allowedPathRulePredicates = map[string]bool{
	"Path":       true,
	"PathPrefix": true,
	"PathRegexp": true,
}

// forbiddenPathRulePredicates are Traefik predicates fibe-distilled owns or deliberately excludes.
var forbiddenPathRulePredicates = map[string]bool{
	"ClientIP":      true,
	"Headers":       true,
	"HeadersRegexp": true,
	"Host":          true,
	"HostRegexp":    true,
	"HostSNI":       true,
	"HostSNIRegexp": true,
	"Method":        true,
	"Query":         true,
}

// ValidationErrors validates Fibe labels and unsupported fields for one Compose service.
func ValidationErrors(name string, definition Definition) []string {
	labels := NormalizeLabels(definition.Labels)
	errors := validateUnsupportedServiceRuntimeFields(name, definition)
	return append(errors, validateServiceLabels(name, definition, labels)...)
}

// validateServiceLabels validates Fibe labels for one Compose service.
func validateServiceLabels(name string, definition Definition, labels map[string]string) []string {
	errors := make([]string, 0, 4)
	errors = append(errors, validateFibeLabelRawTypes(name, definition.Labels)...)
	errors = append(errors, validateLabelMap(name, labels, validateFibeLabelValue)...)
	errors = append(errors, validateSourceLabels(name, definition, labels)...)
	errors = append(errors, validateRoutingLabels(name, labels)...)
	return errors
}

// validateUnsupportedServiceRuntimeFields rejects Compose file-resolution features.
func validateUnsupportedServiceRuntimeFields(name string, definition Definition) []string {
	if _, ok := definition.Raw["env_file"]; !ok {
		return nil
	}
	return []string{fmt.Sprintf("Service %q: env_file is not implemented in fibe-distilled; env-file resolution is outside fibe-distilled because fibe-distilled does not fetch or upload env files", name)}
}

// validateFibeLabelRawTypes rejects map-form Fibe labels with inert scalar types.
func validateFibeLabelRawTypes(name string, rawLabels any) []string {
	switch typed := rawLabels.(type) {
	case []any:
		return validateLabelListTypes(name, typed)
	case map[any]any:
		return validateAnyMapLabelTypes(name, typed)
	default:
		labels, ok := AsMap(rawLabels)
		if !ok {
			return nil
		}
		return validateLabelMap(name, labels, validateRawLabelType)
	}
}

// validateAnyMapLabelTypes rejects loose map labels with non-string keys.
func validateAnyMapLabelTypes(name string, labels map[any]any) []string {
	errors := make([]string, 0, 1)
	stringLabels := make(map[string]any, len(labels))
	hasNonStringKey := false
	for key, value := range labels {
		text, ok := stringMapKey(key)
		if !ok {
			hasNonStringKey = true
			continue
		}
		stringLabels[text] = value
	}
	if hasNonStringKey {
		errors = append(errors, fmt.Sprintf("Service %q: label map keys must be strings", name))
	}
	return append(errors, validateLabelMap(name, stringLabels, validateRawLabelType)...)
}

// validateLabelMap collects validation errors for one label map.
func validateLabelMap[T any](name string, labels map[string]T, validate func(string, string, T) string) []string {
	var errors []string
	for key, value := range labels {
		if err := validate(name, key, value); err != "" {
			errors = append(errors, err)
		}
	}
	return errors
}

// validateLabelListTypes rejects non-string items in Compose label lists.
func validateLabelListTypes(name string, labels []any) []string {
	var errors []string
	for idx, value := range labels {
		if _, ok := value.(string); !ok {
			errors = append(errors, fmt.Sprintf("Service %q: labels[%d] must be a string label item", name, idx))
		}
	}
	return errors
}

// validateRawLabelType checks one map-form label key and value shape.
func validateRawLabelType(name string, key string, value any) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Sprintf("Service %q: label key must not be blank", name)
	}
	if !rawLabelScalarAllowed(value) {
		return fmt.Sprintf("Service %q: label %q must be a string, number, or boolean value", name, key)
	}
	if isTraefikRouterMiddlewareLabel(key) && !rawLabelString(value) {
		return fmt.Sprintf("Service %q: label %q must be a string value", name, key)
	}
	return validateRawFibeLabelType(name, key, value)
}

// rawLabelScalarAllowed reports whether a map-form label value can become metadata.
func rawLabelScalarAllowed(value any) bool {
	switch value.(type) {
	case string, bool, int, int64, float64:
		return true
	default:
		return false
	}
}

// rawLabelString reports whether one raw map-form label is already a string.
func rawLabelString(value any) bool {
	_, ok := value.(string)
	return ok
}

// isTraefikRouterMiddlewareLabel reports whether fibe-distilled must parse a Traefik middleware list label.
func isTraefikRouterMiddlewareLabel(key string) bool {
	return strings.HasPrefix(key, traefikRouterPrefix) && strings.HasSuffix(key, ".middlewares")
}

// validateRawFibeLabelType checks the value type for one map-form Fibe label.
func validateRawFibeLabelType(name string, key string, value any) string {
	if !strings.HasPrefix(key, "fibe.gg/") || rawFibeLabelTypeAllowed(key, value) {
		return ""
	}
	return fmt.Sprintf("Service %q: label %q must be a string value", name, key)
}

// rawFibeLabelTypeAllowed reports non-string label values fibe-distilled understands.
func rawFibeLabelTypeAllowed(key string, value any) bool {
	switch value.(type) {
	case string:
		return true
	case bool:
		return booleanFibeLabels[key]
	case int, int64, float64:
		return key == fibeLabelPort
	default:
		return false
	}
}

// validateFibeLabelValue returns the validation error for one normalized label.
func validateFibeLabelValue(name string, key string, value string) string {
	if !isFibeLabel(key) {
		return ""
	}
	if reason := unsupportedFibeLabelReasons[key]; reason != "" {
		return fmt.Sprintf("Service %q: label %q %s", name, key, reason)
	}
	if !allowedFibeLabels[key] {
		return fmt.Sprintf("Service %q: unknown label %q", name, key)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Sprintf("Service %q: label %q must not be blank", name, key)
	}
	if booleanFibeLabels[key] && !fibetemplate.ContainsVariable(value) && !validBoolLabelValue(value) {
		return fmt.Sprintf("Service %q: label %q must be boolean true or false", name, key)
	}
	return ""
}

// isFibeLabel reports whether a Compose label belongs to the Fibe namespace.
func isFibeLabel(key string) bool {
	return strings.HasPrefix(key, "fibe.gg/")
}

// validateSourceLabels validates repository/build label combinations.
func validateSourceLabels(name string, definition Definition, labels map[string]string) []string {
	repoURL := strings.TrimSpace(labels["fibe.gg/repo_url"])
	errors := validateSourceRepositoryLabels(name, definition, labels, repoURL)
	if err := validateSourceBranchLabel(name, labels[fibeLabelBranch]); err != "" {
		errors = append(errors, err)
	}
	errors = append(errors, validateRepoPathLabels(name, labels)...)
	if err := validateBuildArgsLabel(name, labels[fibeLabelBuildArgs]); err != "" {
		errors = append(errors, err)
	}
	if err := validateSourceMountLabel(name, labels["fibe.gg/source_mount"]); err != "" {
		errors = append(errors, err)
	}
	return errors
}

// validateSourceRepositoryLabels checks source labels that depend on repo_url.
func validateSourceRepositoryLabels(name string, definition Definition, labels map[string]string, repoURL string) []string {
	var errors []string
	if repoURL != "" && git.RepositoryURLHasCredentials(repoURL) {
		errors = append(errors, fmt.Sprintf("Service %q: fibe.gg/repo_url must not include credentials; use process credentials or SSH access", name))
	}
	if invalidCloneableRepositoryURL(repoURL) {
		errors = append(errors, fmt.Sprintf("Service %q: fibe.gg/repo_url must be a cloneable Git URL or SSH target", name))
	}
	if definition.Build != nil && repoURL == "" {
		errors = append(errors, fmt.Sprintf("Service %q: build requires fibe.gg/repo_url", name))
	}
	if strings.TrimSpace(labels["fibe.gg/source_mount"]) != "" && repoURL == "" {
		errors = append(errors, fmt.Sprintf("Service %q: fibe.gg/source_mount requires fibe.gg/repo_url", name))
	}
	return errors
}

// invalidCloneableRepositoryURL reports repository labels that cannot be used by git clone.
func invalidCloneableRepositoryURL(repoURL string) bool {
	return repoURL != "" && !fibetemplate.ContainsVariable(repoURL) && !git.CloneableRepositoryURL(repoURL)
}

// validateRepoPathLabels checks labels that reference files inside a repository.
func validateRepoPathLabels(name string, labels map[string]string) []string {
	var errors []string
	for _, label := range []string{"fibe.gg/dockerfile"} {
		if err := validateRepoPathLabel(name, label, labels[label]); err != "" {
			errors = append(errors, err)
		}
	}
	return errors
}

// validateRepoPathLabel checks labels that reference files inside a repository.
func validateRepoPathLabel(name string, label string, value string) string {
	if validRepoRelativePath(value) {
		return ""
	}
	return fmt.Sprintf("Service %q: label %q must be a relative repository path without parent traversal", name, label)
}

// validateSourceBranchLabel checks fibe.gg/branch before runtime Git commands use it.
func validateSourceBranchLabel(name string, value string) string {
	if fibetemplate.ContainsVariable(value) || validGitBranchName(value) {
		return ""
	}
	return fmt.Sprintf("Service %q: label %q must be a valid Git branch name", name, fibeLabelBranch)
}

// validateBuildArgsLabel checks fibe.gg/build_args before Docker build uses it.
func validateBuildArgsLabel(name string, value string) string {
	if strings.TrimSpace(value) == "" || fibetemplate.ContainsVariable(value) || validDockerBuildArgs(value) {
		return ""
	}
	return fmt.Sprintf("Service %q: label %q must contain comma-separated Docker build args in KEY or KEY=VALUE form", name, fibeLabelBuildArgs)
}

// validateSourceMountLabel checks fibe.gg/source_mount target paths.
func validateSourceMountLabel(name string, value string) string {
	if validContainerPath(value) {
		return ""
	}
	return fmt.Sprintf("Service %q: label %q must be an absolute container path without parent traversal", name, "fibe.gg/source_mount")
}

// validateRoutingLabels validates HTTP exposure label combinations.
func validateRoutingLabels(name string, labels map[string]string) []string {
	var errors []string
	if err := validateVisibilityRequiresPort(name, labels); err != "" {
		errors = append(errors, err)
	}
	if err := validatePortLabel(name, labels["fibe.gg/port"]); err != "" {
		errors = append(errors, err)
	}
	if err := validatePathRuleLabel(name, labels["fibe.gg/path_rule"]); err != "" {
		errors = append(errors, err)
	}
	if err := validateVisibilityLabel(name, labels["fibe.gg/visibility"]); err != "" {
		errors = append(errors, err)
	}
	if err := validateSubdomainLabel(name, labels["fibe.gg/subdomain"]); err != "" {
		errors = append(errors, err)
	}
	return errors
}

// validateVisibilityRequiresPort requires an exposed port for visibility.
func validateVisibilityRequiresPort(name string, labels map[string]string) string {
	if strings.TrimSpace(labels["fibe.gg/visibility"]) == "" || strings.TrimSpace(labels["fibe.gg/port"]) != "" {
		return ""
	}
	return fmt.Sprintf("Service %q: fibe.gg/visibility requires fibe.gg/port", name)
}

// validatePortLabel checks that fibe.gg/port is a TCP port number.
func validatePortLabel(name string, rawPort string) string {
	rawPort = strings.TrimSpace(rawPort)
	if rawPort == "" {
		return ""
	}
	if fibetemplate.ContainsVariable(rawPort) {
		return ""
	}
	if validPortEndpoint(rawPort) {
		return ""
	}
	return fmt.Sprintf("Service %q: fibe.gg/port must be between 1 and 65535", name)
}

// validatePathRuleLabel limits fibe.gg/path_rule to path predicates.
func validatePathRuleLabel(name string, pathRule string) string {
	pathRule = strings.TrimSpace(pathRule)
	if pathRule == "" {
		return ""
	}
	predicates := pathRulePredicateNames(pathRule)
	if len(predicates) == 0 {
		if fibetemplate.ContainsVariable(pathRule) {
			return ""
		}
		return fmt.Sprintf("Service %q: fibe.gg/path_rule must use Path, PathPrefix, or PathRegexp", name)
	}
	return validatePathRulePredicates(name, predicates)
}

// validatePathRulePredicates validates parsed Traefik predicate names.
func validatePathRulePredicates(name string, predicates []string) string {
	for _, predicate := range predicates {
		if err := validatePathRulePredicateName(name, predicate); err != "" {
			return err
		}
	}
	return ""
}

// validatePathRulePredicateName validates one parsed Traefik predicate name.
func validatePathRulePredicateName(name string, predicate string) string {
	if forbiddenPathRulePredicates[predicate] {
		return fmt.Sprintf("Service %q: fibe.gg/path_rule cannot use %s", name, predicate)
	}
	if !allowedPathRulePredicates[predicate] {
		return fmt.Sprintf("Service %q: fibe.gg/path_rule must use Path, PathPrefix, or PathRegexp", name)
	}
	return ""
}

// pathRulePredicateNames extracts Traefik predicate names outside quoted strings.
func pathRulePredicateNames(rule string) []string {
	var scanner pathRulePredicateScanner
	return scanner.scan(rule)
}

// pathRulePredicateScanner tracks quoted sections while scanning a rule.
type pathRulePredicateScanner struct {
	names []string
	quote rune
}

// scan returns all predicate names found outside quoted strings.
func (s *pathRulePredicateScanner) scan(rule string) []string {
	for idx, r := range rule {
		if s.consumeQuotedRune(r) {
			continue
		}
		if r == '(' {
			s.addPredicate(rule[:idx])
		}
	}
	return s.names
}

// consumeQuotedRune updates quote state and reports whether the rune is quoted.
func (s *pathRulePredicateScanner) consumeQuotedRune(r rune) bool {
	if s.quote != 0 {
		if r == s.quote {
			s.quote = 0
		}
		return true
	}
	if !isPathRuleQuote(r) {
		return false
	}
	s.quote = r
	return true
}

// addPredicate records the predicate name immediately before a paren.
func (s *pathRulePredicateScanner) addPredicate(prefix string) {
	if name := predicateNameBeforeParen(prefix); name != "" {
		s.names = append(s.names, name)
	}
}

// isPathRuleQuote reports quote runes in Traefik rule strings.
func isPathRuleQuote(r rune) bool {
	return r == '`' || r == '\'' || r == '"'
}

// predicateNameBeforeParen returns the identifier immediately before a predicate paren.
func predicateNameBeforeParen(prefix string) string {
	prefix = strings.TrimRight(prefix, " \t\r\n")
	end := len(prefix)
	start := end
	for start > 0 && isPredicateNameByte(prefix[start-1]) {
		start--
	}
	if start == end {
		return ""
	}
	return prefix[start:end]
}

// isPredicateNameByte reports whether a byte can belong to a Traefik predicate name.
func isPredicateNameByte(value byte) bool {
	return (value >= 'A' && value <= 'Z') ||
		(value >= 'a' && value <= 'z') ||
		(value >= '0' && value <= '9') ||
		value == '_'
}

// validateVisibilityLabel checks the fibe-distilled visibility vocabulary.
func validateVisibilityLabel(name string, visibility string) string {
	visibility = strings.TrimSpace(visibility)
	if visibility == "" || fibetemplate.ContainsVariable(visibility) || visibility == "external" || visibility == "internal" {
		return ""
	}
	return fmt.Sprintf("Service %q: fibe.gg/visibility must be external or internal", name)
}

// validateSubdomainLabel checks one service subdomain label.
func validateSubdomainLabel(name string, subdomain string) string {
	subdomain = strings.TrimSpace(subdomain)
	if subdomain == "" || subdomain == "@" || fibetemplate.ContainsVariable(subdomain) || subdomainPattern.MatchString(subdomain) {
		return ""
	}
	return fmt.Sprintf("Service %q: fibe.gg/subdomain must be a valid subdomain", name)
}

// validBoolLabelValue accepts true or false.
func validBoolLabelValue(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "true" || value == "false"
}

package service

import (
	"fmt"
	"sort"
	"strings"
)

// globalServiceOverrideKey names the launch-wide service override bucket.
const globalServiceOverrideKey = "_global"

// serviceOverrideIssueSet accumulates deterministic override validation paths.
type serviceOverrideIssueSet struct {
	values []string
}

// add records one malformed override path.
func (s *serviceOverrideIssueSet) add(value string) {
	s.values = append(s.values, value)
}

// empty reports whether validation found no issues.
func (s serviceOverrideIssueSet) empty() bool {
	return len(s.values) == 0
}

// joined returns issue paths in deterministic order.
func (s serviceOverrideIssueSet) joined() string {
	values := append([]string(nil), s.values...)
	sort.Strings(values)
	return strings.Join(values, ", ")
}

// OverrideContentIssuePaths returns malformed field paths for one service override object.
func OverrideContentIssuePaths(prefix string, override map[string]any) []string {
	issues := serviceOverrideIssueSet{}
	addInvalidServiceOverrideContent(&issues, prefix, override)
	values := append([]string(nil), issues.values...)
	sort.Strings(values)
	return values
}

// ValidateServiceOverrideNames checks targets and shapes of service overrides.
func ValidateServiceOverrideNames(rendered map[string]any, subdomains map[string]string, overrides map[string]any) error {
	services, ok := AsMap(rendered["services"])
	if !ok {
		return validateOverridesWithoutServices(subdomains, overrides)
	}
	issues := serviceOverrideIssueSet{}
	addInvalidSubdomainTargets(&issues, services, subdomains)
	addInvalidOverrideTargets(&issues, services, overrides)
	addInvalidOverrideContent(&issues, overrides)
	if issues.empty() {
		return nil
	}
	return fmt.Errorf("service overrides reference services not present in compose or malformed override objects: %s", issues.joined())
}

// RejectJobModeServiceOverrides blocks Tricks/job-mode override surfaces.
func RejectJobModeServiceOverrides(overrides map[string]any) error {
	for serviceName, raw := range overrides {
		if serviceName == "_run" {
			return fmt.Errorf("services._run is not implemented in fibe-distilled")
		}
		override, ok := AsMap(raw)
		if !ok {
			continue
		}
		if _, ok := override["job_watch"]; ok {
			return fmt.Errorf("services.%s.job_watch is not implemented in fibe-distilled", serviceName)
		}
	}
	return nil
}

// validateOverridesWithoutServices rejects overrides when Compose has no services.
func validateOverridesWithoutServices(subdomains map[string]string, overrides map[string]any) error {
	issues := serviceOverrideIssueSet{}
	for serviceName, subdomain := range subdomains {
		if issue := invalidSubdomainOverrideIssue(serviceName, subdomain); issue != "" {
			issues.add(issue)
			continue
		}
		issues.add("service_subdomains." + serviceName)
	}
	for serviceName := range overrides {
		if serviceName != globalServiceOverrideKey {
			issues.add("services." + serviceName)
		}
	}
	if issues.empty() {
		return nil
	}
	return fmt.Errorf("service overrides require compose services: %s", issues.joined())
}

// addInvalidSubdomainTargets records service_subdomains for missing services.
func addInvalidSubdomainTargets(issues *serviceOverrideIssueSet, services map[string]any, subdomains map[string]string) {
	for serviceName, subdomain := range subdomains {
		if issue := invalidSubdomainOverrideIssue(serviceName, subdomain); issue != "" {
			issues.add(issue)
			continue
		}
		if !hasServiceMap(services, serviceName) {
			issues.add("service_subdomains." + serviceName)
		}
	}
}

// invalidSubdomainOverrideIssue reports blank launch subdomain overrides.
func invalidSubdomainOverrideIssue(serviceName string, subdomain string) string {
	if isBlank(serviceName) {
		return "service_subdomains.<blank>"
	}
	if isBlank(subdomain) {
		return "service_subdomains." + serviceName
	}
	return ""
}

// addInvalidOverrideTargets records missing or malformed service override targets.
func addInvalidOverrideTargets(issues *serviceOverrideIssueSet, services map[string]any, overrides map[string]any) {
	for serviceName, rawOverride := range overrides {
		if issue := invalidOverrideTargetIssue(services, serviceName, rawOverride); issue != "" {
			issues.add(issue)
		}
	}
}

// invalidOverrideTargetIssue returns the validation path for a bad target.
func invalidOverrideTargetIssue(services map[string]any, serviceName string, rawOverride any) string {
	if serviceName == globalServiceOverrideKey {
		return malformedOverrideIssue(serviceName, rawOverride)
	}
	if isBlank(serviceName) {
		return "services.<blank>"
	}
	if !hasServiceMap(services, serviceName) {
		return "services." + serviceName
	}
	return malformedOverrideIssue(serviceName, rawOverride)
}

// malformedOverrideIssue reports non-object override payloads.
func malformedOverrideIssue(serviceName string, rawOverride any) string {
	if override, ok := AsMap(rawOverride); ok && len(override) > 0 {
		return ""
	}
	return "services." + serviceName
}

// hasServiceMap reports whether a service exists and is mutable.
func hasServiceMap(services map[string]any, serviceName string) bool {
	raw, ok := services[serviceName]
	if !ok {
		return false
	}
	_, ok = AsMap(raw)
	return ok
}

// addInvalidOverrideContent validates fields inside override objects.
func addInvalidOverrideContent(issues *serviceOverrideIssueSet, overrides map[string]any) {
	for serviceName, rawOverride := range overrides {
		override, ok := AsMap(rawOverride)
		if !ok {
			continue
		}
		prefix := "services." + serviceName
		if serviceName == globalServiceOverrideKey {
			addInvalidGlobalOverrideContent(issues, prefix, override)
			continue
		}
		addInvalidServiceOverrideContent(issues, prefix, override)
	}
}

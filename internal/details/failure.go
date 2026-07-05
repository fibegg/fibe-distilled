package details

import (
	"regexp"
	"strings"
)

var (
	// successfulExitPattern identifies benign zero-exit service messages.
	successfulExitPattern = regexp.MustCompile(`container\s+\S+\s+exited\s+\(0\)`)
	// benignLifecycleLinePattern identifies noisy non-error Compose lifecycle lines.
	benignLifecycleLinePattern = regexp.MustCompile(`(?i)\b(Creating|Created|Starting|Started|Waiting|Healthy)\s*$`)
	// retryableInfrastructurePattern identifies transient infrastructure failures.
	retryableInfrastructurePattern = regexp.MustCompile(`connection refused|lost connection|timed out|network.*failure|temporary.*failure|worker.*shutting down`)
	// dockerPullAuthPattern identifies registry authentication failures.
	dockerPullAuthPattern = regexp.MustCompile(`unauthorized|authentication required|pull access denied|requested access.*denied|access forbidden|invalid username or password|no basic auth credentials`)
	// dockerPullMissingPattern identifies registry missing-image failures.
	dockerPullMissingPattern = regexp.MustCompile(`manifest.*not found|manifest unknown|repository does not exist|name unknown`)
	// failedConditionPattern extracts failed Compose conditions.
	failedConditionPattern = regexp.MustCompile(`(?i)condition\s+["']?([^"',\n]+)["']?`)
	// dependencyHintPattern extracts dependency failure details.
	dependencyHintPattern = regexp.MustCompile(`(?i)dependency failed to start:\s*(.+)$`)
	// failedServicePattern extracts a service named by Compose as unsuccessful.
	failedServicePattern = regexp.MustCompile(`(?i)service "([^"]+)" didn't complete successfully`)
	// dependsOnServicePattern extracts a dependency service name from Compose output.
	dependsOnServicePattern = regexp.MustCompile(`(?i)depends on service\s+([a-z0-9_.-]+)`)
	// failedContainerPattern extracts unhealthy or exited container names.
	failedContainerPattern = regexp.MustCompile(`(?i)container\s+["']?([^"'\s,;]+)["']?\s+(?:is unhealthy|exited)`)
)

// composeFailureRule maps one output pattern to a stable diagnostics category.
type composeFailureRule struct {
	category string
	pattern  *regexp.Regexp
}

// composeFailureRules is ordered from specific infrastructure causes to broad failures.
var composeFailureRules = []composeFailureRule{
	{"timeout", regexp.MustCompile(`timed out|timeout`)},
	{"registry_rate_limited", regexp.MustCompile(`toomanyrequests|too many requests|pull rate limit|rate limit`)},
	{"image_pull", regexp.MustCompile(`pull access denied|requested access.*denied|manifest.*not found|manifest unknown|failed to resolve|no matching manifest|repository does not exist|unauthorized|authentication required|access forbidden|invalid username or password|no basic auth credentials|name unknown`)},
	{"port_bind", regexp.MustCompile(`address already in use|port is already allocated|ports are not available`)},
	{"container_conflict", regexp.MustCompile(`conflict.*container|container name.*already in use|container.*already exists`)},
	{"host_init", regexp.MustCompile(`docker-init|init-path`)},
	{"permission", regexp.MustCompile(`permission denied|operation not permitted`)},
	{"network", regexp.MustCompile(`network.*not found|failed to create network|could not find network`)},
	{"invalid_compose", regexp.MustCompile(`yaml|unsupported config option|services.*must be|additional property|validating .*compose`)},
	{"dependency_unhealthy", regexp.MustCompile(`dependency failed|unhealthy|condition service_healthy|depends on service`)},
	{"service_exit", regexp.MustCompile(`didn't complete successfully|exited|exit\s+\d+`)},
	{"volume_mount", regexp.MustCompile(`mount|volume|no such file or directory`)},
}

// composeFailureActions maps categories to short operator next actions.
var composeFailureActions = map[string]string{
	"dependency_unhealthy":  "inspect dependency healthcheck and startup ordering",
	"service_exit":          "inspect service command, environment, and recent logs",
	"image_pull":            "check image name, tag, registry credentials, and network access",
	"registry_rate_limited": "configure Docker Hub credentials for the marquee runtime",
	"port_bind":             "check host port conflicts on the marquee",
	"container_conflict":    "retry current compose or remove stale containers for this compose project",
	"host_init":             "configure Docker init-path on the marquee or remove init: true from the compose services",
	"volume_mount":          "check mounted paths, named volumes, and file permissions",
	"permission":            "check container user, host path permissions, and Docker socket access",
	"network":               "check Docker networks and Traefik network availability",
	"invalid_compose":       "validate generated compose YAML",
	"timeout":               "poll playground status and inspect slow-starting services",
}

// FailureDiagnostics is the structured explanation for a runtime/Compose failure.
type FailureDiagnostics struct {
	// Category is the normalized failure class.
	Category string `json:"category"`
	// FailedService names the service implicated by Compose output.
	FailedService string `json:"failed_service,omitempty"`
	// FailedCondition records the dependency or health condition that failed.
	FailedCondition string `json:"failed_condition,omitempty"`
	// DependencyHint carries Compose dependency failure context.
	DependencyHint string `json:"dependency_hint,omitempty"`
	// ComposeError is the sanitized root Compose error line.
	ComposeError string `json:"compose_error,omitempty"`
	// DockerPullErrorType identifies pull auth, missing image, or rate-limit failures.
	DockerPullErrorType string `json:"docker_pull_error_type,omitempty"`
	// NextActions are concise operator remediation hints.
	NextActions []string `json:"next_actions,omitempty"`
	// RetryableInfrastructure reports whether retrying may succeed without config changes.
	RetryableInfrastructure bool `json:"retryable_infrastructure,omitempty"`
	// DiskFull reports whether output indicates an exhausted disk.
	DiskFull bool `json:"disk_full,omitempty"`
}

// ClassifyComposeFailure maps noisy Compose output into user-facing diagnostics.
func ClassifyComposeFailure(message string, serviceNames []string) FailureDiagnostics {
	clean := stripANSI(message)
	category := composeFailureCategory(clean)
	failedService := composeFailureService(clean, serviceNames)
	return FailureDiagnostics{
		Category:                category,
		FailedService:           failedService,
		FailedCondition:         matchGroup(clean, failedConditionPattern),
		DependencyHint:          matchGroup(clean, dependencyHintPattern),
		ComposeError:            extractComposeError(clean),
		DockerPullErrorType:     dockerPullDiagnostics(clean, category),
		NextActions:             composeFailureNextActions(category, failedService),
		RetryableInfrastructure: retryableInfrastructureFailure(clean),
		DiskFull:                diskFullFailure(clean),
	}
}

// Details converts diagnostics into the API error_details shape.
func (d FailureDiagnostics) Details() map[string]any {
	compose := map[string]any{"category": d.Category}
	d.addComposeDetails(compose)
	out := map[string]any{"compose_failure": compose}
	if d.RetryableInfrastructure {
		out["retryable_infrastructure"] = true
	}
	if d.DiskFull {
		out["disk_full"] = true
	}
	return out
}

// addComposeDetails fills the nested compose_failure diagnostics object.
func (d FailureDiagnostics) addComposeDetails(compose map[string]any) {
	if d.FailedService != "" {
		compose["failed_service"] = d.FailedService
	}
	if d.FailedCondition != "" {
		compose["failed_condition"] = d.FailedCondition
	}
	if d.DependencyHint != "" {
		compose["dependency_hint"] = d.DependencyHint
	}
	if d.ComposeError != "" {
		compose["compose_error"] = d.ComposeError
	}
	if d.DockerPullErrorType != "" {
		compose["docker_pull_error_type"] = d.DockerPullErrorType
	}
	if len(d.NextActions) > 0 {
		compose["next_actions"] = d.NextActions
	}
}

// extractComposeError returns the highest-signal error text from Compose output.
func extractComposeError(stderr string) string {
	clean := stripANSI(stderr)
	lines := nonEmptyLines(clean)
	if len(lines) == 0 {
		return "unknown error"
	}
	if signal := composeErrorLines(lines); len(signal) > 0 {
		return strings.Join(signal, "; ")
	}
	return strings.Join(tailLines(lines, 3), "; ")
}

// composeErrorLines returns non-benign lines that look like failures.
func composeErrorLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if composeErrorLine(line) {
			out = append(out, line)
		}
	}
	return out
}

// composeErrorLine reports whether a Compose output line carries error signal.
func composeErrorLine(line string) bool {
	lower := strings.ToLower(line)
	return !benignLifecycleLinePattern.MatchString(line) &&
		(strings.Contains(lower, "error") ||
			strings.Contains(lower, "failed") ||
			strings.Contains(lower, "exit ") ||
			strings.Contains(lower, "level=error"))
}

// tailLines returns the last count lines, or all lines when shorter.
func tailLines(lines []string, count int) []string {
	start := len(lines) - count
	start = max(start, 0)
	return lines[start:]
}

// retryableInfrastructureFailure reports transient runtime infrastructure errors.
func retryableInfrastructureFailure(message string) bool {
	text := strings.ToLower(message)
	if strings.Contains(text, "clone failed") {
		return false
	}
	return retryableInfrastructurePattern.MatchString(text)
}

// diskFullFailure reports whether output indicates host disk exhaustion.
func diskFullFailure(message string) bool {
	return strings.Contains(strings.ToLower(message), "no space left on device")
}

// dockerPullErrorType classifies pull failures for API diagnostics.
func dockerPullErrorType(message string) string {
	text := strings.ToLower(message)
	if dockerPullAuthPattern.MatchString(text) {
		return "auth_failed"
	}
	if dockerPullMissingPattern.MatchString(text) {
		return "not_found"
	}
	return "unknown"
}

// dockerPullDiagnostics returns a pull-specific subtype when output supports it.
func dockerPullDiagnostics(message string, category string) string {
	kind := dockerPullErrorType(message)
	if kind != "unknown" {
		return kind
	}
	if category == "image_pull" {
		return "unknown"
	}
	return ""
}

// composeFailureCategory picks the first stable category matching Compose output.
func composeFailureCategory(clean string) string {
	text := strings.ToLower(clean)
	textWithoutSuccessfulExits := successfulExitPattern.ReplaceAllString(text, "")
	for _, rule := range composeFailureRules {
		candidate := text
		if rule.category == "service_exit" {
			candidate = textWithoutSuccessfulExits
		}
		if rule.pattern.MatchString(candidate) {
			return rule.category
		}
	}
	return "unknown"
}

// composeFailureService derives the affected service name from Compose output.
func composeFailureService(clean string, serviceNames []string) string {
	if service := matchGroup(clean, failedServicePattern); service != "" {
		return service
	}
	if service := matchGroup(clean, dependsOnServicePattern); service != "" {
		return service
	}
	names := uniqueNames(serviceNames)
	if len(names) == 0 {
		return ""
	}
	matches := failedContainerPattern.FindAllStringSubmatch(clean, -1)
	for _, match := range matches {
		if service := composeFailureContainerService(match[1], names); service != "" {
			return service
		}
	}
	return ""
}

// composeFailureContainerService maps a failed container name to a service.
func composeFailureContainerService(container string, serviceNames []string) string {
	for _, service := range serviceNames {
		if composeContainerMatchesService(container, service) {
			return service
		}
	}
	return ""
}

// composeContainerMatchesService reports Compose container names for a service.
func composeContainerMatchesService(container string, service string) bool {
	suffix := "-" + service + "-"
	index := strings.LastIndex(container, suffix)
	if index >= 0 && decimalSuffix(container[index+len(suffix):]) {
		return true
	}
	if strings.HasPrefix(container, service+"-") {
		return decimalSuffix(container[len(service)+1:])
	}
	return false
}

// decimalSuffix reports whether text is a nonempty decimal suffix.
func decimalSuffix(text string) bool {
	if text == "" {
		return false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// composeFailureNextActions builds deduplicated operator guidance.
func composeFailureNextActions(category string, failedService string) []string {
	var actions []string
	if failedService != "" {
		actions = append(actions, "fetch logs for service "+failedService)
	}
	action := composeFailureActions[category]
	if action == "" {
		action = "retry current compose with diagnostics enabled"
	}
	actions = append(actions, action)
	return dedupe(actions)
}

package service

import "strings"

// addInvalidGlobalOverrideContent validates the _global override object.
func addInvalidGlobalOverrideContent(issues *serviceOverrideIssueSet, prefix string, override map[string]any) {
	for key, value := range override {
		if key != "env_vars" {
			issues.add(prefix + "." + key)
			continue
		}
		checkStringAnyMapOverride(issues, prefix+"."+key, value)
	}
}

// addInvalidServiceOverrideContent validates one named service override object.
func addInvalidServiceOverrideContent(issues *serviceOverrideIssueSet, prefix string, override map[string]any) {
	for key, value := range override {
		checker, ok := serviceOverrideFieldCheckers[key]
		if ok {
			checker(issues, prefix+"."+key, value)
		} else {
			issues.add(prefix + "." + key)
		}
	}
}

// serviceOverrideFieldChecker validates one override field value.
type serviceOverrideFieldChecker func(*serviceOverrideIssueSet, string, any)

// serviceOverrideFieldCheckers maps supported override fields to validators.
var serviceOverrideFieldCheckers = map[string]serviceOverrideFieldChecker{
	"env_vars":            checkStringAnyMapOverride,
	"exposure":            checkExposureMapOverride,
	"git_config":          checkGitConfigMapOverride,
	"port_mappings":       checkPortMappingsOverride,
	"start_command":       checkStartCommandOverride,
	"auth_password":       checkStringOverride,
	"subdomain":           checkStringOverride,
	"exposure_visibility": checkStringOverride,
	"path_rule":           checkStringOverride,
	"dockerfile_path":     checkRepoPathOverride,
	"image":               checkStringOverride,
	"repo_url":            checkStringOverride,
	"build_target":        checkStringOverride,
	"build_args":          checkBuildArgsOverride,
	"production":          checkBoolOverride,
	"exposure_port":       checkPortEndpointOverride,
}

// exposureOverrideFields are accepted fields inside services.<name>.exposure.
var exposureOverrideFields = map[string]bool{
	"enabled":    true,
	"port":       true,
	"subdomain":  true,
	"visibility": true,
	"path_rule":  true,
}

// gitConfigOverrideFields are accepted fields inside services.<name>.git_config.
var gitConfigOverrideFields = map[string]bool{
	"branch_name": true,
}

// checkStringAnyMapOverride requires an object-like value with nonblank keys.
func checkStringAnyMapOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	fields, ok := AsMap(value)
	if !ok || len(fields) == 0 || hasBlankMapKey(fields) {
		issues.add(prefix)
		return
	}
	for _, fieldValue := range fields {
		if _, ok := envOverrideString(fieldValue); !ok {
			issues.add(prefix)
			return
		}
	}
}

// checkExposureMapOverride validates nested exposure fields.
func checkExposureMapOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	fields, ok := validOverrideObject(issues, prefix, value)
	if !ok {
		return
	}
	addUnknownMapFields(issues, prefix, fields, exposureOverrideFields)
	if invalidExposureEnabled(issues, prefix, fields) || exposureDisabled(fields) {
		return
	}
	addInvalidKnownMapFields(issues, prefix, fields, exposureOverrideFields)
}

// validOverrideObject returns a nonempty override object.
func validOverrideObject(issues *serviceOverrideIssueSet, prefix string, value any) (map[string]any, bool) {
	fields, ok := AsMap(value)
	if !ok || len(fields) == 0 {
		issues.add(prefix)
		return nil, false
	}
	return fields, true
}

// addUnknownMapFields records nested fields outside an allowlist.
func addUnknownMapFields(issues *serviceOverrideIssueSet, prefix string, fields map[string]any, allowed map[string]bool) {
	for key := range fields {
		if !allowed[key] {
			issues.add(prefix + "." + key)
		}
	}
}

// invalidExposureEnabled records malformed exposure.enabled values.
func invalidExposureEnabled(issues *serviceOverrideIssueSet, prefix string, fields map[string]any) bool {
	if _, present := fields["enabled"]; !present {
		return false
	}
	if overrideBool(fields, "enabled").ok {
		return false
	}
	issues.add(prefix + ".enabled")
	return true
}

// exposureDisabled reports whether the override explicitly disables routing.
func exposureDisabled(fields map[string]any) bool {
	enabled := overrideBool(fields, "enabled")
	return enabled.ok && !enabled.value
}

// addInvalidKnownMapFields validates allowed nested fields.
func addInvalidKnownMapFields(issues *serviceOverrideIssueSet, prefix string, fields map[string]any, allowed map[string]bool) {
	for key, rawField := range fields {
		if !allowed[key] {
			continue
		}
		if !validMapOverrideField(key, rawField, allowed) {
			issues.add(prefix + "." + key)
		}
	}
}

// checkGitConfigMapOverride validates nested Git config fields.
func checkGitConfigMapOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	addInvalidMapOverrideFields(issues, prefix, value, gitConfigOverrideFields)
}

// checkPortMappingsOverride validates port mapping payloads.
func checkPortMappingsOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	addInvalidPortMappingContent(issues, prefix, value)
}

// checkStartCommandOverride validates accepted command shapes.
func checkStartCommandOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	if !isStartCommandValue(value) {
		issues.add(prefix)
	}
}

// checkStringOverride requires a nonblank string value.
func checkStringOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	if !isStringValue(value) {
		issues.add(prefix)
	}
}

// checkBoolOverride requires a strict boolean value.
func checkBoolOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	if !isBoolOverrideValue(value) {
		issues.add(prefix)
	}
}

// checkRepoPathOverride requires a safe repository-relative file path.
func checkRepoPathOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" || !validRepoRelativePath(text) {
		issues.add(prefix)
	}
}

// checkBuildArgsOverride requires comma-separated Docker build-arg entries.
func checkBuildArgsOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	text, ok := value.(string)
	if !ok || !validDockerBuildArgs(text) {
		issues.add(prefix)
	}
}

// checkPortEndpointOverride requires one valid port endpoint value.
func checkPortEndpointOverride(issues *serviceOverrideIssueSet, prefix string, value any) {
	if _, ok := portEndpointText(value); !ok {
		issues.add(prefix)
	}
}

// addInvalidMapOverrideFields records unsupported or malformed nested fields.
func addInvalidMapOverrideFields(issues *serviceOverrideIssueSet, prefix string, value any, allowed map[string]bool) {
	fields, ok := validOverrideObject(issues, prefix, value)
	if !ok {
		return
	}
	addUnknownMapFields(issues, prefix, fields, allowed)
	addInvalidKnownMapFields(issues, prefix, fields, allowed)
}

// validMapOverrideField validates one nested map override field.
func validMapOverrideField(key string, value any, allowed map[string]bool) bool {
	if !allowed[key] {
		return false
	}
	if key == "branch_name" {
		text, ok := value.(string)
		return ok && strings.TrimSpace(text) != "" && validGitBranchName(text)
	}
	if key == "enabled" {
		return isBoolOverrideValue(value)
	}
	if key == "port" {
		_, ok := portEndpointText(value)
		return ok
	}
	return isStringValue(value)
}

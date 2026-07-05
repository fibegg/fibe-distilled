package service

import (
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// defaultExposurePort is used when exposure is enabled without a port.
const defaultExposurePort = "80"

// overrideLabelMapping maps SDK override fields to fibe.gg labels.
type overrideLabelMapping struct {
	field string
	label string
	port  bool
}

// directLabelMappings map top-level service overrides to labels.
var directLabelMappings = []overrideLabelMapping{
	{field: "subdomain", label: "fibe.gg/subdomain"},
	{field: "exposure_visibility", label: "fibe.gg/visibility"},
	{field: "path_rule", label: "fibe.gg/path_rule"},
	{field: "exposure_port", label: "fibe.gg/port", port: true},
}

// exposureLabelMappings map nested exposure overrides to labels.
var exposureLabelMappings = []overrideLabelMapping{
	{field: "port", label: "fibe.gg/port", port: true},
	{field: "subdomain", label: "fibe.gg/subdomain"},
	{field: "visibility", label: "fibe.gg/visibility"},
	{field: "path_rule", label: "fibe.gg/path_rule"},
}

// serviceMetadataLabelMappings map source/build metadata to labels.
var serviceMetadataLabelMappings = []overrideLabelMapping{
	{field: "dockerfile_path", label: "fibe.gg/dockerfile"},
	{field: "repo_url", label: "fibe.gg/repo_url"},
	{field: "build_target", label: "fibe.gg/build_target"},
}

// exposureLabels are cleared when exposure is explicitly disabled.
var exposureLabels = []string{
	"fibe.gg/port",
	"fibe.gg/subdomain",
	"fibe.gg/path_rule",
	"fibe.gg/visibility",
}

// applyServiceLabelOverrides translates SDK overrides into Compose labels.
func applyServiceLabelOverrides(serviceMap map[string]any, override map[string]any) {
	labels := normalizeLabelsAny(serviceMap["labels"])
	changed := applyDirectLabelOverrides(labels, override)
	changed = applyExposureLabelOverride(labels, override) || changed
	changed = applyGitLabelOverride(labels, override) || changed
	changed = applyMetadataLabelOverrides(labels, override) || changed
	changed = applyStartCommandLabelOverride(labels, override) || changed
	if len(labels) > 0 || changed {
		serviceMap["labels"] = labels
	}
}

// applyDirectLabelOverrides applies direct top-level label mappings.
func applyDirectLabelOverrides(labels map[string]any, override map[string]any) bool {
	return applyLabelMappings(labels, override, directLabelMappings)
}

// applyMetadataLabelOverrides applies build/source label mappings.
func applyMetadataLabelOverrides(labels map[string]any, override map[string]any) bool {
	changed := applyLabelMappings(labels, override, serviceMetadataLabelMappings)
	changed = applyProductionLabelOverride(labels, override) || changed
	return applyBuildArgsLabelOverride(labels, override) || changed
}

// applyLabelMappings writes string label values from mapped fields.
func applyLabelMappings(labels map[string]any, values map[string]any, mappings []overrideLabelMapping) bool {
	changed := false
	for _, mapping := range mappings {
		changed = setLabelString(labels, mapping.label, overrideLabelValue(values, mapping)) || changed
	}
	return changed
}

// overrideLabelValue reads one supported label override field.
func overrideLabelValue(values map[string]any, mapping overrideLabelMapping) string {
	if mapping.port {
		return overridePortString(values, mapping.field)
	}
	return overrideString(values, mapping.field)
}

// applyGitLabelOverride maps git_config.branch_name to fibe.gg/branch.
func applyGitLabelOverride(labels map[string]any, override map[string]any) bool {
	gitConfig, ok := AsMap(override["git_config"])
	if !ok {
		return false
	}
	return setLabelString(labels, "fibe.gg/branch", overrideString(gitConfig, "branch_name"))
}

// applyStartCommandLabelOverride maps start_command to fibe.gg/start_command.
func applyStartCommandLabelOverride(labels map[string]any, override map[string]any) bool {
	rawStartCommand, ok := override["start_command"].(string)
	if !ok {
		return false
	}
	return setLabelString(labels, "fibe.gg/start_command", strings.TrimSpace(rawStartCommand))
}

// applyBuildArgsLabelOverride maps build args to FibeCore-compatible syntax.
func applyBuildArgsLabelOverride(labels map[string]any, override map[string]any) bool {
	rawBuildArgs, ok := override["build_args"].(string)
	if !ok {
		return false
	}
	parts, ok := domain.ParseDockerBuildArgs(rawBuildArgs)
	if !ok || len(parts) == 0 {
		return false
	}
	return setLabelString(labels, "fibe.gg/build_args", strings.Join(parts, ","))
}

// applyProductionLabelOverride maps production mode metadata to a boolean label.
func applyProductionLabelOverride(labels map[string]any, override map[string]any) bool {
	production := overrideBool(override, "production")
	if !production.ok {
		return false
	}
	if production.value {
		labels["fibe.gg/production"] = "true"
		return true
	}
	labels["fibe.gg/production"] = "false"
	return true
}

// applyExposureLabelOverride applies nested exposure label changes.
func applyExposureLabelOverride(labels map[string]any, override map[string]any) bool {
	exposure, ok := AsMap(override["exposure"])
	if !ok {
		return false
	}
	enabled := overrideBool(exposure, "enabled")
	if enabled.ok && !enabled.value {
		clearExposureLabels(labels)
		return true
	}
	changed := defaultExposureLabel(labels, exposure, enabled.ok && enabled.value)
	changed = applyLabelMappings(labels, exposure, exposureLabelMappings) || changed
	return changed
}

// clearExposureLabels removes all fibe-distilled routing labels.
func clearExposureLabels(labels map[string]any) {
	for _, key := range exposureLabels {
		delete(labels, key)
	}
}

// defaultExposureLabel sets the default port when exposure is enabled.
func defaultExposureLabel(labels map[string]any, exposure map[string]any, enabled bool) bool {
	if !enabled {
		return false
	}
	if overridePortString(exposure, "port") != "" || overridePortString(labels, "fibe.gg/port") != "" {
		return false
	}
	labels["fibe.gg/port"] = defaultExposurePort
	return true
}

// setLabelString writes nonblank label values.
func setLabelString(labels map[string]any, key string, value string) bool {
	if value == "" {
		return false
	}
	labels[key] = value
	return true
}

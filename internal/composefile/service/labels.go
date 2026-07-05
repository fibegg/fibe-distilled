package service

import (
	"maps"
	"regexp"
	"strconv"
	"strings"
)

// NormalizeLabels converts Compose map and list label forms into string labels.
func NormalizeLabels(raw any) map[string]string {
	out := map[string]string{}
	switch labels := raw.(type) {
	case map[string]any:
		normalizeStringMapLabels(out, labels)
	case map[any]any:
		normalizeAnyMapLabels(out, labels)
	case []any:
		normalizeRawLabelList(out, labels)
	case []string:
		normalizeStringLabelList(out, labels)
	}
	return out
}

// allowedFibeLabels is the fibe-distilled supported fibe.gg label subset.
var allowedFibeLabels = map[string]bool{
	"fibe.gg/repo_url":      true,
	"fibe.gg/source_mount":  true,
	"fibe.gg/dockerfile":    true,
	"fibe.gg/branch":        true,
	"fibe.gg/start_command": true,
	"fibe.gg/port":          true,
	"fibe.gg/visibility":    true,
	"fibe.gg/subdomain":     true,
	"fibe.gg/path_rule":     true,
	"fibe.gg/production":    true,
	"fibe.gg/build_target":  true,
	"fibe.gg/build_args":    true,
}

// booleanFibeLabels marks supported labels with strict boolean values.
var booleanFibeLabels = map[string]bool{
	"fibe.gg/production": true,
}

// unsupportedFibeLabelReasons explain known Fibe labels outside fibe-distilled scope.
var unsupportedFibeLabelReasons = map[string]string{
	"fibe.gg/env_file":                 "env-file default resolution is outside fibe-distilled; use explicit launch env overrides or Compose environment values",
	"fibe.gg/job_watch":                "is outside fibe-distilled; job-watch services belong to Fibe Tricks/job-mode Playgrounds",
	"fibe.gg/zerodowntime":             "zero-downtime and healthcheck labels are outside fibe-distilled's stateless runtime scope",
	"fibe.gg/healthcheck_path":         "zero-downtime and healthcheck labels are outside fibe-distilled's stateless runtime scope",
	"fibe.gg/healthcheck_interval":     "zero-downtime and healthcheck labels are outside fibe-distilled's stateless runtime scope",
	"fibe.gg/healthcheck_timeout":      "zero-downtime and healthcheck labels are outside fibe-distilled's stateless runtime scope",
	"fibe.gg/healthcheck_retries":      "zero-downtime and healthcheck labels are outside fibe-distilled's stateless runtime scope",
	"fibe.gg/healthcheck_start_period": "zero-downtime and healthcheck labels are outside fibe-distilled's stateless runtime scope",
}

// subdomainPattern is the accepted single-label subdomain shape.
var subdomainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// normalizeLabelsAny converts Compose label forms to a mutable map.
func normalizeLabelsAny(raw any) map[string]any {
	out := map[string]any{}
	switch labels := raw.(type) {
	case map[string]any:
		maps.Copy(out, labels)
	case map[any]any:
		for key, value := range labels {
			if text, ok := stringMapKey(key); ok {
				out[text] = value
			}
		}
	case []any:
		addRawLabelItems(out, labels)
	case []string:
		addLabelItems(out, labels)
	}
	return out
}

// addRawLabelItems adds string labels from a loose YAML list.
func addRawLabelItems(out map[string]any, values []any) {
	for _, value := range values {
		label, ok := value.(string)
		if ok {
			addLabelItem(out, label)
		}
	}
}

// addLabelItems adds string labels from a Compose list.
func addLabelItems(out map[string]any, values []string) {
	for _, value := range values {
		addLabelItem(out, value)
	}
}

// addLabelItem parses one KEY[=VALUE] label into a map.
func addLabelItem(out map[string]any, value string) {
	item := splitLabelItem(value)
	if item.key == "" {
		return
	}
	if !item.hasValue {
		out[item.key] = ""
		return
	}
	out[item.key] = item.value
}

// normalizeStringMapLabels copies map-form labels into the normalized output.
func normalizeStringMapLabels(out map[string]string, labels map[string]any) {
	for key, value := range labels {
		normalizeLabelPair(out, key, value)
	}
}

// normalizeAnyMapLabels stringifies YAML map labels into the normalized output.
func normalizeAnyMapLabels(out map[string]string, labels map[any]any) {
	for key, value := range labels {
		if text, ok := stringMapKey(key); ok {
			normalizeLabelPair(out, text, value)
		}
	}
}

// normalizeRawLabelList normalizes loose YAML list-form labels.
func normalizeRawLabelList(out map[string]string, labels []any) {
	for _, value := range labels {
		label, ok := value.(string)
		if ok {
			normalizeStringLabel(out, label)
		}
	}
}

// normalizeStringLabelList normalizes Compose KEY=VALUE label lists.
func normalizeStringLabelList(out map[string]string, labels []string) {
	for _, label := range labels {
		normalizeStringLabel(out, label)
	}
}

// normalizeStringLabel parses one KEY[=VALUE] label.
func normalizeStringLabel(out map[string]string, label string) {
	item := splitLabelItem(label)
	if item.key == "" {
		return
	}
	if !item.hasValue {
		out[item.key] = ""
		return
	}
	out[item.key] = item.value
}

// labelItem carries one parsed Compose list-form label item.
type labelItem struct {
	key      string
	value    string
	hasValue bool
}

// splitLabelItem parses one Compose list-form label item.
func splitLabelItem(label string) labelItem {
	key, value, hasValue := strings.Cut(label, "=")
	return labelItem{key: strings.TrimSpace(key), value: value, hasValue: hasValue}
}

// normalizeLabelPair writes one map-form label in string form.
func normalizeLabelPair(out map[string]string, k string, v any) {
	k = strings.TrimSpace(k)
	switch typed := v.(type) {
	case string:
		normalizeStringLabelPair(out, k, typed)
	case bool:
		out[k] = strconv.FormatBool(typed)
	case int:
		out[k] = strconv.Itoa(typed)
	case int64:
		out[k] = strconv.FormatInt(typed, 10)
	case float64:
		out[k] = formatFloatScalar(typed)
	default:
		if k != "" {
			out[k] = ""
		}
	}
}

// normalizeStringLabelPair writes string labels and legacy fibe.gg key=value forms.
func normalizeStringLabelPair(out map[string]string, key string, value string) {
	if strings.Contains(value, "=") && strings.HasPrefix(value, "fibe.gg/") {
		parts := strings.SplitN(value, "=", 2)
		out[parts[0]] = parts[1]
		return
	}
	out[key] = value
}

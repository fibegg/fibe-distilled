package compatgate

import (
	"net/http"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"gopkg.in/yaml.v3"
)

// checkUnsupportedComposePayloads rejects full-Fibe Compose features in payload YAML.
func checkUnsupportedComposePayloads(_ *http.Request, route routeSpec, op operationSpec, body map[string]any, bodyParsed bool) []unsupportedItem {
	if !bodyParsed || body == nil {
		return nil
	}
	var out []unsupportedItem
	for field, composeYAML := range composePayloads(route, op, body) {
		out = append(out, unsupportedComposeFeatures(field, composeYAML)...)
	}
	sortUnsupported(out)
	return out
}

// composePayloads extracts Compose YAML fields from supported API payloads.
func composePayloads(route routeSpec, op operationSpec, body map[string]any) map[string]string {
	payload := composePayloadField(route, op, body)
	if payload.value == "" {
		return nil
	}
	return map[string]string{payload.field: payload.value}
}

// composePayloadFieldResult carries one supported Compose-bearing payload field.
type composePayloadFieldResult struct {
	field string
	value string
}

// composePayloadField returns the supported Compose-bearing payload field.
func composePayloadField(route routeSpec, op operationSpec, body map[string]any) composePayloadFieldResult {
	if playspecComposePayload(route, op) {
		return composePayloadFieldResult{field: "field:base_compose_yaml", value: wrappedStringField(body, "playspec", "base_compose_yaml")}
	}
	if launchComposePayload(route, op) {
		return composePayloadFieldResult{field: "field:compose_yaml", value: stringValue(body["compose_yaml"])}
	}
	return composePayloadFieldResult{}
}

// playspecComposePayload reports whether the route can carry Playspec Compose.
func playspecComposePayload(route routeSpec, op operationSpec) bool {
	return route.Resource == "playspecs" && (op.Operation == "create" || op.Operation == "update")
}

// launchComposePayload reports whether the route can carry launch Compose.
func launchComposePayload(route routeSpec, op operationSpec) bool {
	return route.Resource == "launches" && op.Operation == "create"
}

// wrappedStringField reads a string field from a wrapped resource payload.
func wrappedStringField(body map[string]any, wrapper string, field string) string {
	payload := unwrap(body, wrapper)
	if payload == nil {
		return ""
	}
	return stringValue(payload[field])
}

// unsupportedComposeFeatures parses Compose YAML and returns unsupported features.
func unsupportedComposeFeatures(field string, composeYAML string) []unsupportedItem {
	doc := map[string]any{}
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		return nil
	}
	var out []unsupportedItem
	if xFibe, ok := compose.AsMap(doc["x-fibe.gg"]); ok {
		out = append(out, unsupportedXFibeFeatures(field, xFibe)...)
	}
	out = append(out, unsupportedComposeServiceFields(field, doc)...)
	out = append(out, unsupportedComposeServiceLabels(field, doc)...)
	return out
}

// unsupportedComposeServiceFields checks service-level Compose keys outside scope.
func unsupportedComposeServiceFields(field string, doc map[string]any) []unsupportedItem {
	services, ok := compose.AsMap(doc["services"])
	if !ok {
		return nil
	}
	var out []unsupportedItem
	for serviceName, raw := range services {
		service, ok := compose.AsMap(raw)
		if !ok {
			continue
		}
		if _, ok := service["env_file"]; ok {
			out = append(out, unsupportedItem{
				Key:    field + ".services." + serviceName + ".env_file",
				Reason: "Compose env_file is outside fibe-distilled because fibe-distilled does not fetch or upload env files; use explicit launch env_overrides or Compose environment values",
			})
		}
	}
	return out
}

// unsupportedComposeServiceLabels checks service-level fibe.gg labels.
func unsupportedComposeServiceLabels(field string, doc map[string]any) []unsupportedItem {
	services, ok := compose.AsMap(doc["services"])
	if !ok {
		return nil
	}
	var out []unsupportedItem
	for serviceName, raw := range services {
		service, ok := compose.AsMap(raw)
		if !ok {
			continue
		}
		out = append(out, unsupportedComposeLabelItems(field, serviceName, service["labels"])...)
	}
	return out
}

// unsupportedComposeLabelItems converts unsupported labels into field errors.
func unsupportedComposeLabelItems(field string, serviceName string, rawLabels any) []unsupportedItem {
	var out []unsupportedItem
	for key := range service.NormalizeLabels(rawLabels) {
		if reason := unsupportedComposeLabelReason(key); reason != "" {
			out = append(out, unsupportedItem{
				Key:    field + ".services." + serviceName + ".labels." + key,
				Reason: reason,
			})
		}
	}
	return out
}

// unsupportedXFibeKeys are x-fibe.gg keys from full Fibe outside fibe-distilled.
var unsupportedXFibeKeys = []string{"job_mode", "trigger_config", "schedule_config", "muti_config"}

// unsupportedXFibeReasons explains unsupported x-fibe.gg keys.
var unsupportedXFibeReasons = map[string]string{
	"job_mode":        "job-mode templates belong to Fibe Tricks and are not implemented in fibe-distilled",
	"trigger_config":  "scheduled, triggered, and multi-user template execution settings are outside fibe-distilled",
	"schedule_config": "scheduled, triggered, and multi-user template execution settings are outside fibe-distilled",
	"muti_config":     "scheduled, triggered, and multi-user template execution settings are outside fibe-distilled",
}

// unsupportedXFibeFeatures checks root and metadata x-fibe.gg feature blocks.
func unsupportedXFibeFeatures(field string, xFibe map[string]any) []unsupportedItem {
	out := unsupportedXFibeKeyItems(field+".x-fibe.gg.", xFibe)
	if metadata, ok := compose.AsMap(xFibe["metadata"]); ok {
		out = append(out, unsupportedXFibeKeyItems(field+".x-fibe.gg.metadata.", metadata)...)
		out = append(out, unsupportedXFibeMetadataToggles(field, metadata)...)
	}
	return out
}

// unsupportedXFibeKeyItems checks one x-fibe.gg map for unsupported keys.
func unsupportedXFibeKeyItems(prefix string, values map[string]any) []unsupportedItem {
	var out []unsupportedItem
	for _, key := range unsupportedXFibeKeys {
		if _, ok := values[key]; ok {
			out = append(out, unsupportedItem{Key: prefix + key, Reason: unsupportedXFibeReasons[key]})
		}
	}
	return out
}

// unsupportedXFibeMetadataToggles checks true-only metadata capabilities.
func unsupportedXFibeMetadataToggles(field string, metadata map[string]any) []unsupportedItem {
	var out []unsupportedItem
	for key, reason := range map[string]string{
		"preserve_ports":  "preserving raw Compose host ports is outside fibe-distilled; fibe-distilled always strips service ports so Traefik owns routed exposure",
		"source_defaults": "source-default auto-fill belongs to full-Fibe source-backed template imports",
	} {
		if enabled, ok := metadata[key].(bool); ok && enabled {
			out = append(out, unsupportedItem{
				Key:    field + ".x-fibe.gg.metadata." + key,
				Reason: reason,
			})
		}
	}
	return out
}

// unsupportedComposeLabelReason explains unsupported label keys.
func unsupportedComposeLabelReason(key string) string {
	switch key {
	case "fibe.gg/env_file":
		return "env-file default resolution is outside fibe-distilled; use explicit launch env overrides or Compose environment values"
	case "fibe.gg/job_watch":
		return "fibe.gg/job_watch belongs to Fibe Tricks/job-mode Playgrounds and is not implemented in fibe-distilled"
	case "fibe.gg/zerodowntime", "fibe.gg/healthcheck_path", "fibe.gg/healthcheck_interval", "fibe.gg/healthcheck_timeout", "fibe.gg/healthcheck_retries", "fibe.gg/healthcheck_start_period":
		return "zero-downtime and healthcheck labels are outside fibe-distilled's stateless runtime scope"
	default:
		return ""
	}
}

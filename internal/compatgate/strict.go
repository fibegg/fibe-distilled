package compatgate

import (
	"net/http"
	"net/url"
	"strconv"
)

// fieldSet is a constant-time membership set for accepted request fields.
type fieldSet map[string]bool

// checkStrictRequest rejects extra bodies, repeated query params, and unknown fields.
func checkStrictRequest(r *http.Request, route routeSpec, op operationSpec, body map[string]any, bodyParsed bool) []unsupportedItem {
	var out []unsupportedItem
	out = append(out, rejectUnexpectedBody(r, route, op)...)
	out = append(out, rejectRepeatedQuery(r)...)
	out = append(out, rejectUnknownQuery(r, allowedQueryFields(route, op))...)
	out = append(out, rejectUnsupportedQuery(r, route, op)...)
	if bodyParsed {
		out = append(out, rejectUnknownBodyFields(route, op, body)...)
	}
	return out
}

// unsupportedQueryForOperation applies operation-specific query denials.
func unsupportedQueryForOperation(operation string, q url.Values) []unsupportedItem {
	switch operation {
	case "props:list":
		return unsupportedPropProviderQuery(q)
	case "playspecs:list":
		return rejectQueryPresence(q, "job_mode", "job-mode Playspec filtering belongs to Fibe Tricks and is not implemented in fibe-distilled")
	case "playgrounds:list":
		return append(
			rejectQueryPresence(q, "job_mode", "job-mode Playground filtering belongs to Fibe Tricks and is not implemented in fibe-distilled"),
			rejectQueryPresence(q, "result_status", "JobResult filtering belongs to Fibe Tricks and is not implemented in fibe-distilled")...,
		)
	default:
		return nil
	}
}

// unsupportedPropProviderQuery rejects provider selectors outside fibe-distilled scope.
func unsupportedPropProviderQuery(q url.Values) []unsupportedItem {
	var out []unsupportedItem
	for _, provider := range q["provider"] {
		if item, ok := propProviderUnsupportedItem(provider, "query:provider="); ok {
			out = append(out, item)
		}
	}
	return out
}

// rejectQueryPresence rejects an otherwise known query key for an operation.
func rejectQueryPresence(q url.Values, key string, reason string) []unsupportedItem {
	if _, ok := q[key]; !ok {
		return nil
	}
	return []unsupportedItem{{Key: "query:" + key, Reason: reason}}
}

// allowedQueryFields returns query keys accepted for a supported operation.
func allowedQueryFields(route routeSpec, op operationSpec) fieldSet {
	// Documented SDK list params every *ListParams exposes. Handlers apply these
	// filters; unknown params are rejected so accepted query fields cannot become
	// silent no-ops.
	listCommon := []string{"q", "name", "sort", "created_after", "created_before", "page", "per_page"}
	switch route.Resource + ":" + op.Operation {
	case "marquees:list":
		return fields(append(listCommon, "status")...)
	case "props:list":
		return fields(append(listCommon, "status", "provider", "private")...)
	case "playspecs:list":
		return fields(append(listCommon, "job_mode", "locked")...)
	case "playgrounds:list":
		return fields(append(listCommon, "status", "job_mode", "playspec_id", "marquee_id", "result_status")...)
	case "props:branches":
		return fields("query", "limit")
	default:
		return fields()
	}
}

// nestedFieldChecker validates a nested JSON value under a known field.
type nestedFieldChecker func(any, string) []unsupportedItem

// bodyFieldSpec describes the accepted JSON shape for an operation.
type bodyFieldSpec struct {
	root    string
	allowed fieldSet
	nested  map[string]nestedFieldChecker
}

// strictBodyFieldSpecs is the request-body allowlist for strict API gating.
var strictBodyFieldSpecs = map[string]bodyFieldSpec{
	"props:create": {
		root:    "prop",
		allowed: fields("repository_url", "name", "private", "default_branch", "provider", "credentials"),
	},
	"props:update": {
		root:    "prop",
		allowed: fields("repository_url", "name", "private", "default_branch", "provider", "credentials"),
	},
	"playspecs:create": {
		root: "playspec",
		allowed: fields(
			"name", "description", "base_compose_yaml", "persist_volumes", "job_mode", "target_type", "services",
			"trigger_config", "muti_config", "schedule_config", "mounted_files", "credentials",
			"registry_credentials", "source_template_version_id",
		),
		nested: map[string]nestedFieldChecker{"services": checkPlayspecServices},
	},
	"playspecs:update": {
		root: "playspec",
		allowed: fields(
			"name", "description", "base_compose_yaml", "persist_volumes", "job_mode", "target_type", "services",
			"trigger_config", "muti_config", "schedule_config", "mounted_files", "credentials",
			"registry_credentials", "source_template_version_id",
		),
		nested: map[string]nestedFieldChecker{"services": checkPlayspecServices},
	},
	"repo_status_checks:check": {
		allowed: fields("github_urls"),
	},
	"launches:create": {
		allowed: fields(
			"compose_yaml", "name", "repository_url", "job_mode", "target_type",
			"marquee_id", "create_playground", "persist_volumes", "env_overrides", "variables",
			"service_subdomains", "apiSubdomain", "frontendSubdomain", "services", "config_path", "github_ref",
			"github_installation_id", "github_account",
			"prop_mappings", "template_id", "template_id_or_name", "template_version_id",
			"template_body", "import_template_id", "provision_inputs", "provision_missing_props",
			"git_provider",
		),
		nested: map[string]nestedFieldChecker{"services": checkServiceOverrideMap},
	},
	"playgrounds:create": {
		root: "playground",
		allowed: fields(
			"name", "playspec_id", "marquee_id", "expires_at", "never_expire", "env_overrides",
			"services", "job_mode", "target_type", "only_services", "except_services", "build_overrides_yaml",
			"template_version_id", "target_template_version_id", "source_template_version_id",
		),
		nested: map[string]nestedFieldChecker{"services": checkServiceOverrideMap},
	},
	"playgrounds:update": {
		root: "playground",
		allowed: fields(
			"name", "playspec_id", "marquee_id", "expires_at", "never_expire", "env_overrides",
			"services", "job_mode", "target_type", "only_services", "except_services", "build_overrides_yaml",
			"template_version_id", "target_template_version_id", "source_template_version_id",
		),
		nested: map[string]nestedFieldChecker{"services": checkServiceOverrideMap},
	},
	"playgrounds:logs": {
		allowed: fields("service", "tail"),
	},
	"playgrounds:operation": {
		allowed: fields("action_type", "force", "build_overrides_yaml", "playground"),
	},
	"playgrounds:expiration": {
		allowed: fields("duration_hours"),
	},
}

// checkPlayspecServices validates services[] metadata in Playspec payloads.
func checkPlayspecServices(value any, prefix string) []unsupportedItem {
	services, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []unsupportedItem
	allowed := fields(
		"name", "type", "prop_id", "workdir", "workflow",
		"repo_url", "env_file_path", "dockerfile_path", "start_command", "build_target", "build_args",
		"image", "exposure", "production", "job_watch",
	)
	for idx, raw := range services {
		service, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		itemPrefix := prefix + "." + strconv.Itoa(idx)
		out = append(out, rejectMapFields(service, allowed, itemPrefix+".", map[string]nestedFieldChecker{
			"exposure": checkExposureMap,
		})...)
		out = append(out, rejectUnsupportedPlayspecServiceFields(service, itemPrefix)...)
	}
	return out
}

// rejectUnsupportedPlayspecServiceFields rejects full-Fibe service classifiers.
func rejectUnsupportedPlayspecServiceFields(service map[string]any, prefix string) []unsupportedItem {
	var out []unsupportedItem
	reason := "dynamic Playspec service classification fields are outside fibe-distilled; declare runtime source/build behavior with Compose build, fibe.gg/repo_url, and fibe.gg/source_mount labels"
	for _, key := range []string{"prop_id", "workdir", "workflow"} {
		if _, ok := service[key]; ok {
			out = append(out, unsupportedItem{Key: prefix + "." + key, Reason: reason})
		}
	}
	if _, ok := service["job_watch"]; ok {
		out = append(out, unsupportedItem{Key: prefix + ".job_watch", Reason: "job-watch services belong to Fibe Tricks/job-mode Playgrounds and are not implemented in fibe-distilled"})
	}
	if _, ok := service["env_file_path"]; ok {
		out = append(out, unsupportedItem{Key: prefix + ".env_file_path", Reason: "env-file default resolution is outside fibe-distilled; use explicit launch env overrides or Compose environment values"})
	}
	return out
}

// checkServiceOverrideMap validates service override maps for launches/playgrounds.
func checkServiceOverrideMap(value any, prefix string) []unsupportedItem {
	services, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	var out []unsupportedItem
	for serviceName, raw := range services {
		out = append(out, checkServiceOverrideEntry(serviceName, raw, prefix+"."+serviceName)...)
	}
	return out
}

// checkServiceOverrideEntry validates one service override object.
func checkServiceOverrideEntry(serviceName string, raw any, prefix string) []unsupportedItem {
	if serviceName == "_run" {
		return []unsupportedItem{{Key: prefix, Reason: "services._run belongs to Fibe job-mode Playgrounds and is not implemented in fibe-distilled"}}
	}
	override, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if serviceName == "_global" {
		return rejectMapFields(override, globalServiceOverrideFields, prefix+".", nil)
	}
	return checkRegularServiceOverrideMap(override, prefix)
}

// checkRegularServiceOverrideMap validates one named service override.
func checkRegularServiceOverrideMap(override map[string]any, prefix string) []unsupportedItem {
	out := make([]unsupportedItem, 0, 2)
	out = append(out, rejectMapFields(override, regularServiceOverrideFields, prefix+".", serviceOverrideNestedFields)...)
	out = append(out, rejectUnsupportedServiceOverrideFields(override, prefix)...)
	return out
}

// globalServiceOverrideFields are the supported services._global override fields.
var globalServiceOverrideFields = fields("env_vars")

// regularServiceOverrideFields are the supported named-service override fields.
var regularServiceOverrideFields = fields(
	"subdomain", "exposure_visibility", "path_rule", "start_command", "dockerfile_path",
	"env_file_path", "image", "env_vars", "auth_password", "exposure", "git_config", "port_mappings",
	"repo_url", "build_target", "build_args", "exposure_port", "healthcheck_path", "healthcheck_interval", "healthcheck_timeout",
	"healthcheck_retries", "healthcheck_start_period", "zerodowntime", "job_watch",
)

// serviceOverrideNestedFields validates nested named-service override maps.
var serviceOverrideNestedFields = map[string]nestedFieldChecker{
	"exposure":      checkExposureMap,
	"git_config":    checkGitConfigMap,
	"port_mappings": checkPortMappings,
}

// checkExposureMap validates nested exposure override fields.
func checkExposureMap(value any, prefix string) []unsupportedItem {
	payload, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return rejectMapFields(payload, fields("enabled", "port", "subdomain", "visibility", "path_rule"), prefix+".", nil)
}

// checkGitConfigMap validates supported branch-selection override fields.
func checkGitConfigMap(value any, prefix string) []unsupportedItem {
	payload, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	var out []unsupportedItem
	out = append(out, rejectMapFields(payload, fields("branch_name", "base_branch_name", "create_branch"), prefix+".", nil)...)
	if _, ok := payload["base_branch_name"]; ok {
		out = append(out, unsupportedItem{Key: prefix + ".base_branch_name", Reason: "branch creation/rebase helpers are outside fibe-distilled; provide an existing branch_name"})
	}
	if _, ok := payload["create_branch"]; ok {
		out = append(out, unsupportedItem{Key: prefix + ".create_branch", Reason: "branch creation helpers are outside fibe-distilled; provide an existing branch_name"})
	}
	return out
}

// checkPortMappings validates port mapping override objects.
func checkPortMappings(value any, prefix string) []unsupportedItem {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []unsupportedItem
	for idx, raw := range items {
		payload, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, rejectMapFields(payload, fields("container", "host"), prefix+"."+strconv.Itoa(idx)+".", nil)...)
	}
	return out
}

// rejectUnsupportedServiceOverrideFields rejects job/zero-downtime override fields.
func rejectUnsupportedServiceOverrideFields(override map[string]any, prefix string) []unsupportedItem {
	var out []unsupportedItem
	if _, ok := override["job_watch"]; ok {
		out = append(out, unsupportedItem{Key: prefix + ".job_watch", Reason: "job-watch service overrides belong to Fibe Tricks/job-mode Playgrounds and are not implemented in fibe-distilled"})
	}
	if _, ok := override["env_file_path"]; ok {
		out = append(out, unsupportedItem{Key: prefix + ".env_file_path", Reason: "env-file default resolution is outside fibe-distilled; use explicit launch env overrides or Compose environment values"})
	}
	for _, key := range []string{"zerodowntime", "healthcheck_path", "healthcheck_interval", "healthcheck_timeout", "healthcheck_retries", "healthcheck_start_period"} {
		if _, ok := override[key]; ok {
			out = append(out, unsupportedItem{Key: prefix + "." + key, Reason: "zero-downtime and healthcheck override parameters are outside fibe-distilled's stateless runtime scope"})
		}
	}
	return out
}

// fields builds a fieldSet from accepted JSON or query keys.
func fields(keys ...string) fieldSet {
	out := make(fieldSet, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out
}

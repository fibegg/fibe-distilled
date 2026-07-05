package compatgate

import (
	"net/http"
	"strings"
)

// checkPropBody rejects Prop payload fields tied to excluded providers/secrets.
func checkPropBody(_ *http.Request, body map[string]any) []unsupportedItem {
	payload := unwrap(body, "prop")
	var out []unsupportedItem
	if item, ok := propProviderUnsupportedItem(stringValue(payload["provider"]), "field:provider="); ok {
		out = append(out, item)
	}
	out = appendIfPresent(out, payload, "credentials", "per-Prop credentials are outside fibe-distilled; GitHub access uses the process GITHUB_TOKEN")
	return out
}

// checkPlayspecBody rejects full-Fibe Playspec capabilities.
func checkPlayspecBody(_ *http.Request, body map[string]any) []unsupportedItem {
	payload := unwrap(body, "playspec")
	var out []unsupportedItem
	out = appendIfPresent(out, payload, "job_mode", "job-mode Playspecs belong to Fibe Tricks and are not implemented in fibe-distilled")
	out = appendIfPresent(out, payload, "target_type", "target_type belongs to full-Fibe compose/template and Tricks flows outside fibe-distilled")
	out = appendIfPresent(out, payload, "trigger_config", "triggered Playspecs require webhook/provider flows outside fibe-distilled")
	out = appendIfPresent(out, payload, "muti_config", "multi-user template interaction is outside fibe-distilled")
	out = appendIfPresent(out, payload, "schedule_config", "scheduled Playspec execution is outside fibe-distilled")
	out = appendIfPresent(out, payload, "mounted_files", "hosted mounted-file management is outside fibe-distilled")
	out = appendIfPresent(out, payload, "credentials", "per-Playspec registry credentials are outside fibe-distilled; use process-level DockerHub credentials")
	out = appendIfPresent(out, payload, "registry_credentials", "per-Playspec registry credentials are outside fibe-distilled; use process-level DockerHub credentials")
	out = appendIfPresent(out, payload, "source_template_version_id", "template marketplace lineage is outside fibe-distilled")
	out = appendIfTrue(out, payload, "persist_volumes", "fibe-distilled Playgrounds and Playspecs are stateless; persistent-volume Playspec mode is not supported")
	return out
}

// checkLaunchBody rejects launch capabilities outside supplied-Compose launches.
func checkLaunchBody(_ *http.Request, body map[string]any) []unsupportedItem {
	var out []unsupportedItem
	out = appendIfPresent(out, body, "job_mode", "job-mode launch belongs to Fibe Tricks and is not implemented in fibe-distilled")
	out = appendIfPresent(out, body, "target_type", "target_type belongs to full-Fibe compose/template and Tricks flows outside fibe-distilled")
	if stringValue(body["repository_url"]) != "" && stringValue(body["compose_yaml"]) == "" {
		out = append(out, unsupportedItem{Key: "field:repository_url", Reason: "launch requires caller-supplied compose_yaml; fibe-distilled does not fetch Compose files from repositories"})
	}
	out = appendIfPresent(out, body, "config_path", "repository config-file launch is outside fibe-distilled; send caller-supplied compose_yaml instead")
	out = appendIfPresent(out, body, "github_ref", "repository config-ref launch is outside fibe-distilled; send caller-supplied compose_yaml instead")
	out = appendIfPresent(out, body, "apiSubdomain", "apiSubdomain is ambiguous and service-name-specific; use service_subdomains keyed by the actual Compose service name")
	out = appendIfPresent(out, body, "frontendSubdomain", "frontendSubdomain is ambiguous and service-name-specific; use service_subdomains keyed by the actual Compose service name")
	out = appendIfPresent(out, body, "github_installation_id", "GitHub App installations are outside fibe-distilled; use repository_url with the process GITHUB_TOKEN")
	out = appendIfPresent(out, body, "github_account", "GitHub App account selection is outside fibe-distilled; use repository_url with the process GITHUB_TOKEN")
	out = appendIfPresent(out, body, "prop_mappings", "template Prop mapping/provisioning is outside fibe-distilled; use explicit Compose labels and stored Props")
	out = appendIfPresent(out, body, "template_id", "template marketplace launch is outside fibe-distilled; launch from compose_yaml or repository_url")
	out = appendIfPresent(out, body, "template_id_or_name", "template marketplace launch is outside fibe-distilled; launch from compose_yaml or repository_url")
	out = appendIfPresent(out, body, "template_version_id", "template marketplace launch is outside fibe-distilled; launch from compose_yaml or repository_url")
	out = appendIfPresent(out, body, "template_body", "template marketplace launch is outside fibe-distilled; launch from compose_yaml or repository_url")
	out = appendIfPresent(out, body, "import_template_id", "template marketplace launch is outside fibe-distilled; launch from compose_yaml or repository_url")
	out = appendIfPresent(out, body, "provision_inputs", "managed Git-service provisioning is outside fibe-distilled")
	out = appendIfPresent(out, body, "provision_missing_props", "managed Git-service provisioning is outside fibe-distilled")
	out = appendIfTrue(out, body, "persist_volumes", "fibe-distilled Playgrounds and Playspecs are stateless; persistent-volume Playspec mode is not supported")
	if _, ok := body["git_provider"]; ok {
		value := stringValue(body["git_provider"])
		if strings.EqualFold(value, excludedGitProviderName) {
			out = append(out, unsupportedItem{Key: "field:git_provider=" + excludedGitProviderName, Reason: "this git provider is excluded from fibe-distilled"})
		} else {
			out = append(out, unsupportedItem{Key: "field:git_provider", Reason: "launch provider selection is outside fibe-distilled; use repository_url with caller-supplied compose_yaml"})
		}
	}
	return out
}

// checkPlaygroundBody rejects Playground capabilities outside runtime scope.
func checkPlaygroundBody(_ *http.Request, body map[string]any) []unsupportedItem {
	payload := unwrap(body, "playground")
	var out []unsupportedItem
	out = appendIfPresent(out, payload, "job_mode", "job-mode Playgrounds belong to Fibe Tricks and are not implemented in fibe-distilled")
	out = appendIfPresent(out, payload, "target_type", "target_type belongs to full-Fibe compose/template and Tricks flows outside fibe-distilled")
	out = appendIfPresent(out, payload, "only_services", "job-mode service selectors belong to Fibe Tricks and are not implemented in fibe-distilled")
	out = appendIfPresent(out, payload, "except_services", "job-mode service selectors belong to Fibe Tricks and are not implemented in fibe-distilled")
	out = appendIfPresent(out, payload, "build_overrides_yaml", "build_overrides_yaml is outside fibe-distilled; use compose build labels for target and args")
	out = appendIfPresent(out, payload, "template_version_id", "template switching is outside fibe-distilled")
	out = appendIfPresent(out, payload, "target_template_version_id", "template switching is outside fibe-distilled")
	out = appendIfPresent(out, payload, "source_template_version_id", "template lineage is outside fibe-distilled")
	return out
}

// checkPlaygroundOperationBody rejects unsupported Playground actions and fields.
func checkPlaygroundOperationBody(_ *http.Request, body map[string]any) []unsupportedItem {
	action := stringValue(body["action_type"])
	var out []unsupportedItem
	switch action {
	case "enable_maintenance", "disable_maintenance":
		out = append(out, unsupportedItem{Key: "action:" + action, Reason: "maintenance mode is not part of fibe-distilled runtime scope"})
	case "switch_template", "template_switch", "switch_template_version":
		out = append(out, unsupportedItem{Key: "action:" + action, Reason: "template switching is outside fibe-distilled"})
	}
	out = appendIfPresent(out, body, "build_overrides_yaml", "build_overrides_yaml is outside fibe-distilled; use compose build labels for target and args")
	if _, ok := body["playground"]; ok {
		out = append(out, unsupportedItem{Key: "field:playground", Reason: "nested playground operation payloads are outside fibe-distilled; use top-level action_type and force"})
	}
	return out
}

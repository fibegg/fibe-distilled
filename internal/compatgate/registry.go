package compatgate

import "net/http"

// operationSpec describes one supported operation for a route/method pair.
type operationSpec struct {
	Operation string
	Check     func(*http.Request, map[string]any) []unsupportedItem
}

// routeSpec describes a supported API route family.
type routeSpec struct {
	Segments []string
	Resource string
	Methods  map[string]operationSpec
}

// unsupportedRoute describes a known route that should fail before handlers.
type unsupportedRoute struct {
	Segments  []string
	Methods   []string
	Resource  string
	Operation string
	Reason    string
	Supported []string
}

// unsupportedSurface describes a known full-Fibe resource family.
type unsupportedSurface struct {
	Resource  string
	Operation string
	Reason    string
	Supported []string
}

// routes returns fibe-distilled's supported SDK-compatible API subset.
func routes() []routeSpec {
	return []routeSpec{
		{Segments: []string{"me"}, Resource: "auth", Methods: methods(method(http.MethodGet, "me", nil))},
		{Segments: []string{"status"}, Resource: "status", Methods: methods(method(http.MethodGet, "status", nil))},
		{Segments: []string{"server-info"}, Resource: "server-info", Methods: methods(method(http.MethodGet, "show", nil))},
		{Segments: []string{"async_requests", ":id"}, Resource: "async_requests", Methods: methods(method(http.MethodGet, "show", nil))},

		{Segments: []string{"marquees"}, Resource: "marquees", Methods: methods(method(http.MethodGet, "list", nil))},
		{Segments: []string{"marquees", ":identifier"}, Resource: "marquees", Methods: methods(method(http.MethodGet, "show", nil))},

		{Segments: []string{"props"}, Resource: "props", Methods: methods(method(http.MethodGet, "list", nil), method(http.MethodPost, "create", checkPropBody))},
		{Segments: []string{"props", ":identifier"}, Resource: "props", Methods: methods(method(http.MethodGet, "show", nil), method(http.MethodPatch, "update", checkPropBody), method(http.MethodDelete, "delete", nil))},
		{Segments: []string{"props", ":identifier", "branches"}, Resource: "props", Methods: methods(method(http.MethodGet, "branches", nil))},
		{Segments: []string{"props", ":identifier", "syncs"}, Resource: "props", Methods: methods(method(http.MethodPost, "sync", nil))},
		{Segments: []string{"repo_status_checks"}, Resource: "repo_status_checks", Methods: methods(method(http.MethodPost, "check", nil))},

		{Segments: []string{"playspecs"}, Resource: "playspecs", Methods: methods(method(http.MethodGet, "list", nil), method(http.MethodPost, "create", checkPlayspecBody))},
		{Segments: []string{"playspecs", ":identifier"}, Resource: "playspecs", Methods: methods(method(http.MethodGet, "show", nil), method(http.MethodPatch, "update", checkPlayspecBody), method(http.MethodDelete, "delete", nil))},
		{Segments: []string{"playspecs", ":identifier", "services"}, Resource: "playspecs", Methods: methods(method(http.MethodGet, "services", nil))},

		{Segments: []string{"launches"}, Resource: "launches", Methods: methods(method(http.MethodPost, "create", checkLaunchBody))},

		{Segments: []string{"playgrounds"}, Resource: "playgrounds", Methods: methods(method(http.MethodGet, "list", nil), method(http.MethodPost, "create", checkPlaygroundBody))},
		{Segments: []string{"playgrounds", ":identifier"}, Resource: "playgrounds", Methods: methods(method(http.MethodGet, "show", nil), method(http.MethodPatch, "update", checkPlaygroundBody), method(http.MethodDelete, "delete", nil))},
		{Segments: []string{"playgrounds", ":identifier", "status"}, Resource: "playgrounds", Methods: methods(method(http.MethodGet, "status", nil), method(http.MethodPost, "status_refresh", nil))},
		{Segments: []string{"playgrounds", ":identifier", "logs"}, Resource: "playgrounds", Methods: methods(method(http.MethodPost, "logs", nil))},
		{Segments: []string{"playgrounds", ":identifier", "operations"}, Resource: "playgrounds", Methods: methods(method(http.MethodPost, "operation", checkPlaygroundOperationBody))},
		{Segments: []string{"playgrounds", ":identifier", "expiration"}, Resource: "playgrounds", Methods: methods(method(http.MethodPost, "expiration", nil))},
	}
}

// unsupportedRoutes returns explicit full-Fibe route denials.
func unsupportedRoutes() []unsupportedRoute {
	return []unsupportedRoute{
		{Segments: []string{"compose_validations"}, Resource: "compose_validations", Operation: "validate", Reason: "public Compose validation is outside fibe-distilled; Playspec, Launch, and runtime writes validate Compose internally"},
		{Segments: []string{"marquees"}, Methods: []string{http.MethodPost}, Resource: "marquees", Operation: "create", Reason: configuredMarqueeReason(), Supported: supportedMarqueeDiscovery()},
		{Segments: []string{"marquees", ":identifier"}, Methods: []string{http.MethodPatch}, Resource: "marquees", Operation: "update", Reason: configuredMarqueeReason(), Supported: supportedMarqueeDiscovery()},
		{Segments: []string{"marquees", ":identifier"}, Methods: []string{http.MethodDelete}, Resource: "marquees", Operation: "delete", Reason: configuredMarqueeReason(), Supported: supportedMarqueeDiscovery()},
		{Segments: []string{"marquees", ":identifier", "connection_tests"}, Resource: "marquees", Operation: "connection_test", Reason: configuredMarqueeReason(), Supported: []string{"startup performs local Docker, Compose, /opt/fibe, and Traefik checks"}},
		{Segments: []string{"marquees", ":identifier", "ssh_keys"}, Resource: "marquees", Operation: "ssh_key_generation", Reason: configuredMarqueeReason(), Supported: []string{"external host keys are not managed; fibe-distilled manages the local Docker host mounted into the container"}},
		{Segments: []string{"props", "attachments"}, Methods: []string{http.MethodPost}, Resource: "props", Operation: "attach", Reason: "GitHub App repository attachment is outside fibe-distilled; create a Prop with repository_url and the process GITHUB_TOKEN"},
		{Segments: []string{"props", ":identifier", "sync"}, Methods: []string{http.MethodPost}, Resource: "props", Operation: "sync", Reason: "the Fibe API Prop sync route is /api/props/:id/syncs; fibe-distilled does not expose the web/UI singular sync alias", Supported: []string{"POST /api/props/:id/syncs"}},
		{Segments: []string{"props", ":identifier", "env_defaults"}, Resource: "props", Operation: "env_defaults", Reason: "fibe-distilled does not fetch env files from GitHub; provide explicit launch env overrides or Compose environment values", Supported: []string{"GET /api/props/:id/branches", "POST /api/props/:id/syncs"}},
		{Segments: []string{"props", "mirrors"}, Methods: []string{http.MethodPost}, Resource: "props", Operation: "mirror", Reason: "managed repository mirroring/provisioning is outside fibe-distilled; create a Prop from an existing git repository URL"},
		{Segments: []string{"playspecs", ":identifier", "template_switch_previews"}, Resource: "playspecs", Operation: "template_switch_preview", Reason: "template marketplace and template-version switching are outside fibe-distilled runtime scope"},
		{Segments: []string{"playspecs", ":identifier", "template_switches"}, Resource: "playspecs", Operation: "template_switch", Reason: "template marketplace and template-version switching are outside fibe-distilled runtime scope"},
		{Segments: []string{"playspecs", ":identifier", "mounts"}, Resource: "playspecs", Operation: "mounted_files", Reason: "hosted mounted-file management is outside fibe-distilled runtime scope"},
		{Segments: []string{"playspecs", ":identifier", "registry_credentials"}, Resource: "playspecs", Operation: "registry_credentials", Reason: "fibe-distilled uses process-level DockerHub credentials instead of per-Playspec registry credentials"},
		{Segments: []string{"playgrounds", ":identifier", "compose"}, Resource: "playgrounds", Operation: "compose", Reason: "public generated-Compose inspection is outside fibe-distilled; generated Compose remains internal runtime state"},
		{Segments: []string{"playgrounds", ":identifier", "debug"}, Resource: "playgrounds", Operation: "debug", Reason: "public debug inspection is outside fibe-distilled; use status, logs, and stored error details"},
		{Segments: []string{"playgrounds", ":identifier", "env_metadata"}, Resource: "playgrounds", Operation: "env_metadata", Reason: "public environment metadata inspection is outside fibe-distilled; use Playground service and env fields from supported resource responses"},
		{Segments: []string{"playgrounds", ":identifier", "template_switches"}, Resource: "playgrounds", Operation: "template_switch", Reason: "template marketplace and brownfield template switching are outside fibe-distilled runtime scope"},
	}
}

// unsupportedSurfaces returns full-Fibe resource families outside fibe-distilled.
func unsupportedSurfaces() map[string]unsupportedSurface {
	return map[string]unsupportedSurface{
		excludedAutomationResource + "_defaults": {Resource: excludedAutomationResource + "s", Operation: "defaults", Reason: "this resource family is outside fibe-distilled minimal scope"},
		excludedAutomationResource + "s":         {Resource: excludedAutomationResource + "s", Operation: "api", Reason: "this resource family is outside fibe-distilled minimal scope"},
		"api_keys":                               {Resource: "api_keys", Operation: "crud", Reason: "fibe-distilled uses one startup bearer token and does not manage API keys"},
		"artefacts":                              {Resource: "artefacts", Operation: "api", Reason: "artefact APIs are outside fibe-distilled minimal scope"},
		"audit_logs":                             {Resource: "audit_logs", Operation: "list", Reason: "multi-tenant audit logging is outside fibe-distilled minimal scope"},
		"autoconnect_tokens":                     {Resource: "marquees", Operation: "autoconnect_token", Reason: configuredMarqueeReason()},
		"billing":                                {Resource: "billing", Operation: "api", Reason: "billing is outside fibe-distilled minimal scope"},
		"categories":                             {Resource: "templates", Operation: "categories", Reason: "template marketplace categories are outside fibe-distilled minimal scope"},
		"checkout_sessions":                      {Resource: "billing", Operation: "checkout", Reason: "billing is outside fibe-distilled minimal scope"},
		"events":                                 {Resource: "events", Operation: "monitor", Reason: "live monitor/event stream APIs are outside fibe-distilled minimal scope"},
		"feedbacks":                              {Resource: "feedbacks", Operation: "api", Reason: "feedback APIs are outside fibe-distilled minimal scope"},
		excludedGitProviderName:                  {Resource: excludedGitProviderName, Operation: "api", Reason: "this git provider is excluded from fibe-distilled"},
		excludedGitProviderName + "_repositories": {Resource: excludedGitProviderName, Operation: "repositories", Reason: "this git provider is excluded from fibe-distilled"},
		"github_apps":              {Resource: "github_apps", Operation: "api", Reason: "GitHub App and OAuth flows are outside fibe-distilled; use the process GITHUB_TOKEN"},
		"github_repositories":      {Resource: "github_repositories", Operation: "discovery", Reason: "GitHub App repository discovery is outside fibe-distilled; use repository_url and GITHUB_TOKEN"},
		"github_token":             {Resource: "github_token", Operation: "installation_token", Reason: "GitHub App installation tokens are outside fibe-distilled; use the process GITHUB_TOKEN"},
		"greenfields":              {Resource: "greenfields", Operation: "create", Reason: "managed greenfield provisioning and templates are outside fibe-distilled minimal scope"},
		"import_template_versions": {Resource: "templates", Operation: "versions", Reason: "Bazaar/import-template flows are outside fibe-distilled minimal scope"},
		"import_templates":         {Resource: "templates", Operation: "api", Reason: "Bazaar/import-template flows are outside fibe-distilled minimal scope"},
		"installations":            {Resource: "installations", Operation: "api", Reason: "GitHub App installations are outside fibe-distilled; use repository_url and GITHUB_TOKEN"},
		"job_env":                  {Resource: "job_env", Operation: "api", Reason: "fibe-distilled does not manage global job environment secrets"},
		"job_envs":                 {Resource: "job_env", Operation: "api", Reason: "fibe-distilled does not manage global job environment secrets"},
		"memories":                 {Resource: "memories", Operation: "api", Reason: "memory APIs are outside fibe-distilled minimal scope"},
		"monitors":                 {Resource: "monitors", Operation: "api", Reason: "monitoring surfaces are outside fibe-distilled minimal scope"},
		"mutters":                  {Resource: "mutters", Operation: "api", Reason: "mutter APIs are outside fibe-distilled minimal scope"},
		"oauth":                    {Resource: "oauth", Operation: "api", Reason: "OAuth flows are outside fibe-distilled minimal scope"},
		"players":                  {Resource: "players", Operation: "api", Reason: "multi-user account lifecycle is outside fibe-distilled minimal scope"},
		"secrets":                  {Resource: "secrets", Operation: "api", Reason: "secret management is outside fibe-distilled minimal scope"},
		"subscriptions":            {Resource: "billing", Operation: "subscriptions", Reason: "billing is outside fibe-distilled minimal scope"},
		"teams":                    {Resource: "teams", Operation: "api", Reason: "teams and multi-tenancy are outside fibe-distilled minimal scope"},
		"template_categories":      {Resource: "templates", Operation: "categories", Reason: "template marketplace categories are outside fibe-distilled minimal scope"},
		"template_sources":         {Resource: "templates", Operation: "sources", Reason: "template source publishing is outside fibe-distilled minimal scope"},
		"template_versions":        {Resource: "templates", Operation: "versions", Reason: "template marketplace versions are outside fibe-distilled minimal scope"},
		"templates":                {Resource: "templates", Operation: "api", Reason: "template marketplace APIs are outside fibe-distilled minimal scope"},
		"tricks":                   {Resource: "tricks", Operation: "api", Reason: "Fibe Tricks/job-mode Playgrounds are outside fibe-distilled scope"},
		"webhook_deliveries":       {Resource: "webhooks", Operation: "deliveries", Reason: "webhook delivery is outside fibe-distilled minimal scope"},
		"webhook_endpoints":        {Resource: "webhooks", Operation: "endpoints", Reason: "webhook endpoints are outside fibe-distilled minimal scope"},
		"webhook_event_types":      {Resource: "webhooks", Operation: "event_types", Reason: "webhooks are outside fibe-distilled minimal scope"},
		"webhooks":                 {Resource: "webhooks", Operation: "api", Reason: "webhooks are outside fibe-distilled minimal scope"},
	}
}

// configuredMarqueeReason explains why Marquee management APIs are unsupported.
func configuredMarqueeReason() string {
	return "fibe-distilled uses one startup-configured local Marquee from FIBE_ROOT_DOMAIN, FIBE_ACME_EMAIL, and optional FIBE_BUILD_PLATFORM; the runtime is the local /var/run/docker.sock plus /opt/fibe, HTTPS is always enabled, and per-Marquee Docker credential changes are unavailable"
}

// supportedMarqueeDiscovery lists the remaining read-only Marquee API surface.
func supportedMarqueeDiscovery() []string {
	return []string{"GET /api/marquees", "GET /api/marquees/:id"}
}

// method builds one method entry for a routeSpec.
func method(methodName string, operation string, check func(*http.Request, map[string]any) []unsupportedItem) methodEntry {
	return methodEntry{Method: methodName, Spec: operationSpec{Operation: operation, Check: check}}
}

// methodEntry pairs an HTTP method with its operation spec.
type methodEntry struct {
	Method string
	Spec   operationSpec
}

// methods builds a method registry map for a routeSpec.
func methods(entries ...methodEntry) map[string]operationSpec {
	out := make(map[string]operationSpec, len(entries))
	for _, entry := range entries {
		out[entry.Method] = entry.Spec
	}
	return out
}

package compatgate

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCheckAllowsNonAPIAndSupportedRoutes(t *testing.T) {
	gate := New()
	for _, req := range []*http.Request{
		newGateRequest(t, http.MethodGet, "/up.json", ""),
		newGateRequest(t, http.MethodGet, "/api/me", ""),
		newGateRequest(t, http.MethodPost, "/api/playgrounds/demo/status", ""),
	} {
		if decision := gate.Check(req); !decision.Allowed {
			t.Fatalf("%s %s should be allowed: %#v", req.Method, req.URL.Path, decision)
		}
	}
}

func TestCheckRejectsUnsupportedEndpointFamily(t *testing.T) {
	tests := []struct {
		path     string
		resource string
	}{
		{path: "/api/api_keys", resource: "api_keys"},
		{path: "/api/events?per_page=10", resource: "events"},
	}
	for _, tt := range tests {
		decision := New().Check(newGateRequest(t, http.MethodGet, tt.path, ""))
		if decision.Allowed {
			t.Fatalf("%s should not be allowed", tt.path)
		}
		if decision.Status != http.StatusNotImplemented || decision.Code != "NOT_IMPLEMENTED" {
			t.Fatalf("unexpected decision for %s: %#v", tt.path, decision)
		}
		if decision.Details["resource"] != tt.resource {
			t.Fatalf("unexpected details for %s: %#v", tt.path, decision.Details)
		}
	}
}

func TestCheckRejectsUnsupportedMethodForSupportedRoute(t *testing.T) {
	decision := New().Check(newGateRequest(t, http.MethodPost, "/api/me", "{}"))
	if decision.Allowed {
		t.Fatal("POST /api/me should not be allowed")
	}
	if decision.Details["operation"] != "method" {
		t.Fatalf("unexpected details: %#v", decision.Details)
	}
	methods := decision.Details["supported_methods"].([]string)
	if len(methods) != 1 || methods[0] != http.MethodGet {
		t.Fatalf("unexpected supported methods: %#v", methods)
	}
}

func TestCheckRejectsUnsupportedNestedRoutes(t *testing.T) {
	for _, req := range []struct {
		method    string
		path      string
		operation string
	}{
		{method: http.MethodPost, path: "/api/compose_validations", operation: "validate"},
		{method: http.MethodPost, path: "/api/marquees/demo/ssh_keys", operation: "ssh_key_generation"},
		{method: http.MethodPost, path: "/api/playspecs/demo/template_switches", operation: "template_switch"},
		{method: http.MethodPost, path: "/api/playspecs/demo/mounts", operation: "mounted_files"},
		{method: http.MethodPost, path: "/api/playspecs/demo/registry_credentials", operation: "registry_credentials"},
		{method: http.MethodGet, path: "/api/playgrounds/demo/compose", operation: "compose"},
		{method: http.MethodGet, path: "/api/playgrounds/demo/debug?include=runtime", operation: "debug"},
		{method: http.MethodGet, path: "/api/playgrounds/demo/env_metadata", operation: "env_metadata"},
	} {
		decision := New().Check(newGateRequest(t, req.method, req.path, "{}"))
		if decision.Allowed {
			t.Fatalf("%s should not be allowed", req.path)
		}
		if decision.Code != "NOT_IMPLEMENTED" {
			t.Fatalf("unexpected decision for %s: %#v", req.path, decision)
		}
		if decision.Details["operation"] != req.operation {
			t.Fatalf("unexpected operation for %s: %#v", req.path, decision.Details)
		}
	}
}

func TestCheckPrefersSpecificUnsupportedRoutesOverIdentifierRoutes(t *testing.T) {
	tests := []struct {
		path      string
		operation string
	}{
		{path: "/api/props/attachments", operation: "attach"},
		{path: "/api/props/mirrors", operation: "mirror"},
	}
	for _, tt := range tests {
		decision := New().Check(newGateRequest(t, http.MethodPost, tt.path, "{}"))
		if decision.Allowed {
			t.Fatalf("%s should not be allowed", tt.path)
		}
		if decision.Details["resource"] != "props" || decision.Details["operation"] != tt.operation {
			t.Fatalf("unexpected details for %s: %#v", tt.path, decision.Details)
		}
	}

	if decision := New().Check(newGateRequest(t, http.MethodGet, "/api/props/attachments", "")); !decision.Allowed {
		t.Fatalf("GET /api/props/attachments should still be a prop name-or-ID lookup: %#v", decision)
	}
}

func TestCheckRejectsUnsupportedBodyFields(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		want string
	}{
		{
			name: "playspec trigger config",
			path: "/api/playspecs",
			body: `{"playspec":{"name":"job","trigger_config":{"event_type":"pull_request"}}}`,
			want: "field:trigger_config",
		},
		{
			name: "playspec dynamic prop classification",
			path: "/api/playspecs",
			body: `{"playspec":{"name":"dynamic","base_compose_yaml":"services:\n  web:\n    image: nginx\n","services":[{"name":"web","type":"dynamic","prop_id":12,"workdir":"/app","workflow":"build"}]}}`,
			want: "field:services.0.prop_id",
		},
		{
			name: "launch installation",
			path: "/api/launches",
			body: `{"repository_url":"https://github.com/acme/demo","github_installation_id":123}`,
			want: "field:github_installation_id",
		},
		{
			name: "launch repository config fields",
			path: "/api/launches",
			body: `{"repository_url":"https://github.com/acme/demo","config_path":"deploy/fibe.yml","github_ref":"main"}`,
			want: "field:config_path",
		},
		{
			name: "launch provider selector",
			path: "/api/launches",
			body: `{"compose_yaml":"services:\n  web:\n    image: nginx\n","repository_url":"https://github.com/acme/demo","git_provider":"github"}`,
			want: "field:git_provider",
		},
		{
			name: "launch provider selector null",
			path: "/api/launches",
			body: `{"compose_yaml":"services:\n  web:\n    image: nginx\n","repository_url":"https://github.com/acme/demo","git_provider":null}`,
			want: "field:git_provider",
		},
		{
			name: "launch target type trick",
			path: "/api/launches",
			body: `{"compose_yaml":"services:\n  worker:\n    image: alpine\n","target_type":"trick"}`,
			want: "field:target_type",
		},
		{
			name: "playground build overrides",
			path: "/api/playgrounds",
			body: `{"playground":{"name":"pg","build_overrides_yaml":{"web":{}}}}`,
			want: "field:build_overrides_yaml",
		},
		{
			name: "playground target type trick",
			path: "/api/playgrounds",
			body: `{"playground":{"name":"pg","playspec_id":1,"target_type":"trick"}}`,
			want: "field:target_type",
		},
		{
			name: "playground branch creation false toggle",
			path: "/api/playgrounds",
			body: `{"playground":{"name":"pg","playspec_id":1,"services":{"web":{"git_config":{"create_branch":false}}}}}`,
			want: "field:services.web.git_config.create_branch",
		},
		{
			name: "playground reserved run selector map",
			path: "/api/playgrounds",
			body: `{"playground":{"name":"pg","playspec_id":1,"services":{"_run":{"only_services":["web"]}}}}`,
			want: "field:services._run",
		},
		{
			name: "playground global override unsupported field",
			path: "/api/playgrounds",
			body: `{"playground":{"name":"pg","playspec_id":1,"services":{"_global":{"image":"nginx"}}}}`,
			want: "field:services._global.image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := New().Check(newGateRequest(t, http.MethodPost, tt.path, tt.body))
			if decision.Allowed {
				t.Fatal("request should not be allowed")
			}
			if !containsString(decision.Details["unsupported"].([]string), tt.want) {
				t.Fatalf("unsupported details = %#v, want %q", decision.Details["unsupported"], tt.want)
			}
		})
	}
}

func TestCheckRejectsMarqueeManagementRoutes(t *testing.T) {
	for _, req := range []*http.Request{
		newGateRequest(t, http.MethodPost, "/api/marquees", `{"marquee":{"name":"host"}}`),
		newGateRequest(t, http.MethodPost, "/api/marquees", `{"marquee":{"name":"host","domains_input":"apps.example.test","https_enabled":false,"tls_certificate_source":"provided","tls_certificate_pem":"cert","tls_private_key_pem":"key","dockerhub_auth_enabled":true,"dockerhub_username":"u","dockerhub_token":"t","dns_provider":"cloudflare","dns_credentials":{"token":"secret"},"build_platform":"linux/amd64","prop_id":1}}`),
		newGateRequest(t, http.MethodPatch, "/api/marquees/default", `{"marquee":{"name":"host"}}`),
		newGateRequest(t, http.MethodDelete, "/api/marquees/default", ""),
		newGateRequest(t, http.MethodPost, "/api/marquees/default/connection_tests", ""),
		newGateRequest(t, http.MethodPost, "/api/marquees/default/ssh_keys", ""),
		newGateRequest(t, http.MethodPost, "/api/marquees/default/certificates", ""),
		newGateRequest(t, http.MethodPost, "/api/marquees/default/dns_records", ""),
		newGateRequest(t, http.MethodPatch, "/api/marquees/default/docker_credentials", `{"dockerhub_username":"u"}`),
		newGateRequest(t, http.MethodGet, "/api/marquees/default/status", ""),
		newGateRequest(t, http.MethodPost, "/api/autoconnect_tokens", `{"domain":"apps.example.test","ssl_mode":"letsencrypt"}`),
	} {
		decision := New().Check(req)
		if decision.Allowed {
			t.Fatalf("%s %s should not be allowed", req.Method, req.URL.Path)
		}
		if decision.Code != "NOT_IMPLEMENTED" || decision.Details["resource"] != "marquees" {
			t.Fatalf("unexpected decision: %#v", decision)
		}
	}
}

func TestCheckRejectsPlayspecServiceClassificationFields(t *testing.T) {
	for _, tc := range []struct {
		name           string
		body           string
		wants          []string
		reasonContains string
	}{
		{
			name:           "dynamic service classification",
			body:           `{"playspec":{"name":"dynamic","base_compose_yaml":"services:\n  web:\n    image: nginx\n","services":[{"name":"web","type":"dynamic","prop_id":12,"workdir":"/app","workflow":"build"}]}}`,
			wants:          []string{"field:services.0.prop_id", "field:services.0.workdir", "field:services.0.workflow"},
			reasonContains: "fibe.gg/repo_url",
		},
		{
			name:  "job watch service metadata",
			body:  `{"playspec":{"name":"job","base_compose_yaml":"services:\n  worker:\n    image: alpine\n","services":[{"name":"worker","type":"static","job_watch":true}]}}`,
			wants: []string{"field:services.0.job_watch"},
		},
		{
			name:           "target type trick",
			body:           `{"playspec":{"name":"job","base_compose_yaml":"services:\n  worker:\n    image: alpine\n","target_type":"trick"}}`,
			wants:          []string{"field:target_type"},
			reasonContains: "Tricks",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := assertGateUnsupported(t, http.MethodPost, "/api/playspecs", tc.body, tc.wants...)
			if tc.reasonContains != "" {
				assertDecisionReasonContains(t, decision, tc.reasonContains)
			}
		})
	}

	allowed := New().Check(newGateRequest(t, http.MethodPost, "/api/playspecs", `{"playspec":{"name":"dynamic","base_compose_yaml":"services:\n  web:\n    image: nginx\n","services":[{"name":"web","type":"dynamic","repo_url":"https://github.com/acme/demo.git","dockerfile_path":"Dockerfile","start_command":"npm run dev","build_target":"runner","build_args":"NODE_VERSION=22","production":true,"image":"nginx","exposure":{"enabled":true,"port":80,"subdomain":"web","visibility":"external"}}]}}`))
	if !allowed.Allowed {
		t.Fatalf("supported Playspec service metadata should pass: %#v", allowed)
	}
}

func TestCheckRejectsUnsupportedPropProvider(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		want     string
	}{
		{name: "excluded provider", provider: excludedGitProviderName, want: "field:provider=" + excludedGitProviderName},
		{name: "unknown provider", provider: "bitbucket", want: "field:provider=bitbucket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := New().Check(newGateRequest(t, http.MethodPost, "/api/props", `{"prop":{"name":"p","repository_url":"ssh://git.example.test/acme/p.git","provider":"`+tc.provider+`"}}`))
			if decision.Allowed {
				t.Fatal("unsupported prop provider should not be allowed")
			}
			if !containsString(decision.Details["unsupported"].([]string), tc.want) {
				t.Fatalf("unsupported details = %#v", decision.Details["unsupported"])
			}
		})
	}

	for _, req := range []*http.Request{
		newGateRequest(t, http.MethodPost, "/api/props", `{"prop":{"name":"p","repository_url":"https://github.com/acme/p.git","provider":"github"}}`),
		newGateRequest(t, http.MethodPost, "/api/props", `{"prop":{"name":"p","repository_url":"ssh://git.example.test/acme/p.git","provider":"git"}}`),
		newGateRequest(t, http.MethodPatch, "/api/props/p", `{"prop":{"name":"p-renamed"}}`),
		newGateRequest(t, http.MethodGet, "/api/props/p-renamed", ""),
		newGateRequest(t, http.MethodDelete, "/api/props/p-renamed", ""),
	} {
		if decision := New().Check(req); !decision.Allowed {
			t.Fatalf("%s %s should be allowed for GitHub prop CRUD: %#v", req.Method, req.URL.Path, decision)
		}
	}
}

func TestCheckRejectsExtraBodyFieldsAndPreservesBody(t *testing.T) {
	body := `{"prop":{"name":"p","repository_url":"https://github.com/acme/p.git","unknown_client_field":"kept"}}`
	req := newGateRequest(t, http.MethodPost, "/api/props", body)
	decision := New().Check(req)
	if decision.Allowed {
		t.Fatal("extra body field should not be allowed")
	}
	if !containsString(decision.Details["unsupported"].([]string), "field:unknown_client_field") {
		t.Fatalf("unsupported details = %#v", decision.Details["unsupported"])
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(raw) != body {
		t.Fatalf("body was not restored:\n got %q\nwant %q", raw, body)
	}
}

func TestCheckRejectsExtraQueryFields(t *testing.T) {
	// Standard SDK list params (pagination + documented filters/sort) are allowed.
	for _, q := range []string{
		"/api/playgrounds?status=running&per_page=50&page=1",
		"/api/playgrounds?sort=created_at_desc&created_after=x&created_before=y",
		"/api/props?q=foo&provider=github&sort=name_asc",
		"/api/marquees?name=host&sort=name_asc",
		"/api/playspecs?locked=false",
	} {
		assertGateAllowed(t, http.MethodGet, q, "")
	}
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "unknown query key", path: "/api/playgrounds?status=running&bogus_filter=x", want: "query:bogus_filter"},
		{name: "repeated query key", path: "/api/playgrounds?status=running&status=error", want: "query:status[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertGateUnsupported(t, http.MethodGet, tc.path, "", tc.want)
		})
	}
	for _, q := range []string{
		"/api/playgrounds?result_status=success",
		"/api/playgrounds?job_mode=true",
		"/api/playspecs?job_mode=true",
		"/api/props?provider=" + excludedGitProviderName,
		"/api/props?provider=bitbucket",
	} {
		decision := New().Check(newGateRequest(t, http.MethodGet, q, ""))
		if decision.Allowed {
			t.Fatalf("%s should not be allowed", q)
		}
		if decision.Code != "NOT_IMPLEMENTED" {
			t.Fatalf("%s code = %s, want NOT_IMPLEMENTED", q, decision.Code)
		}
	}
}

func TestCheckRejectsBodyForMethodsWithoutPayloads(t *testing.T) {
	decision := New().Check(newGateRequest(t, http.MethodGet, "/api/me", `{"ignored":true}`))
	if decision.Allowed {
		t.Fatal("GET body should not be allowed")
	}
	if !containsString(decision.Details["unsupported"].([]string), "body") {
		t.Fatalf("unsupported details = %#v", decision.Details["unsupported"])
	}
}

func TestCheckRejectsBodyForBodylessPostOperations(t *testing.T) {
	for _, req := range []*http.Request{
		newGateRequest(t, http.MethodPost, "/api/props/demo/syncs", `{}`),
		newGateRequest(t, http.MethodPost, "/api/playgrounds/demo/status", `{}`),
	} {
		decision := New().Check(req)
		if decision.Allowed {
			t.Fatalf("%s %s body should not be allowed", req.Method, req.URL.Path)
		}
		if !containsString(decision.Details["unsupported"].([]string), "body") {
			t.Fatalf("unsupported details = %#v", decision.Details["unsupported"])
		}
	}
}

func TestCheckRejectsPropSyncWebAlias(t *testing.T) {
	decision := New().Check(newGateRequest(t, http.MethodPost, "/api/props/demo/sync", ""))
	if decision.Allowed {
		t.Fatal("singular prop sync alias should not be allowed")
	}
	if decision.Code != "NOT_IMPLEMENTED" || decision.Details["operation"] != "sync" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if !containsString(decision.Details["supported"].([]string), "POST /api/props/:id/syncs") {
		t.Fatalf("supported details = %#v", decision.Details["supported"])
	}
}

func TestCheckAllowsOptionalBodiesForPayloadBackedPostOperations(t *testing.T) {
	for _, req := range []*http.Request{
		newGateRequest(t, http.MethodPost, "/api/playgrounds/demo/logs", `{}`),
		newGateRequest(t, http.MethodPost, "/api/playgrounds/demo/operations", `{}`),
		newGateRequest(t, http.MethodPost, "/api/playgrounds/demo/expiration", `{}`),
	} {
		if decision := New().Check(req); !decision.Allowed {
			t.Fatalf("%s %s optional body should be allowed: %#v", req.Method, req.URL.Path, decision)
		}
	}
}

func TestCheckRejectsUnknownLengthBodyForMethodsWithoutPayloads(t *testing.T) {
	req := newGateRequest(t, http.MethodGet, "/api/me", "")
	req.Body = io.NopCloser(strings.NewReader(""))
	req.ContentLength = -1

	decision := New().Check(req)
	if decision.Allowed {
		t.Fatal("GET request with explicit unknown-length body should not be allowed")
	}
	if !containsString(decision.Details["unsupported"].([]string), "body") {
		t.Fatalf("unsupported details = %#v", decision.Details["unsupported"])
	}
}

func TestCheckAllowsSupportedBodyAndPreservesBody(t *testing.T) {
	body := `{"prop":{"name":"p","repository_url":"https://github.com/acme/p.git","provider":"github"}}`
	req := newGateRequest(t, http.MethodPost, "/api/props", body)
	decision := New().Check(req)
	if !decision.Allowed {
		t.Fatalf("supported body should be allowed: %#v", decision)
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(raw) != body {
		t.Fatalf("body was not restored:\n got %q\nwant %q", raw, body)
	}
}

func TestCheckRestoredBodyClosesOriginalBody(t *testing.T) {
	body := `{"prop":{"name":"p","repository_url":"https://github.com/acme/p.git","provider":"github"}}`
	original := &trackingReadCloser{Reader: strings.NewReader(body)}
	req := newGateRequest(t, http.MethodPost, "/api/props", "")
	req.Body = original
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")

	decision := New().Check(req)
	if !decision.Allowed {
		t.Fatalf("supported body should be allowed: %#v", decision)
	}
	if err := req.Body.Close(); err != nil {
		t.Fatalf("close restored body: %v", err)
	}
	if !original.closed {
		t.Fatal("closing restored body did not close original body")
	}
}

func TestCheckAllowsServiceAuthPasswordOverride(t *testing.T) {
	body := `{"playground":{"name":"pg","playspec_id":1,"services":{"web":{"auth_password":"service-secret","git_config":{"branch_name":"main"}}}}}`
	decision := New().Check(newGateRequest(t, http.MethodPost, "/api/playgrounds", body))
	if !decision.Allowed {
		t.Fatalf("service auth_password and branch_name overrides should be allowed: %#v", decision)
	}
}

func TestCheckAllowsDynamicServiceOverrides(t *testing.T) {
	dynamicOverrides := `"services":{"web":{"repo_url":"https://github.com/acme/demo.git","dockerfile_path":"deploy/Dockerfile","build_target":"runner","build_args":"NODE_VERSION=22"}}`
	for _, req := range []*http.Request{
		newGateRequest(t, http.MethodPost, "/api/playgrounds", `{"playground":{"name":"pg","playspec_id":1,`+dynamicOverrides+`}}`),
		newGateRequest(t, http.MethodPatch, "/api/playgrounds/pg", `{"playground":{`+dynamicOverrides+`}}`),
		newGateRequest(t, http.MethodPost, "/api/launches", `{"name":"pg","compose_yaml":"services:\n  web:\n    image: nginx\n",`+dynamicOverrides+`}`),
	} {
		decision := New().Check(req)
		if !decision.Allowed {
			t.Fatalf("%s %s dynamic service overrides should be allowed: %#v", req.Method, req.URL.Path, decision)
		}
	}
}

func TestCheckAllowsLaunchServiceSubdomains(t *testing.T) {
	body := `{"name":"pg","repository_url":"https://github.com/acme/demo","compose_yaml":"services:\n  alpha:\n    image: nginx\n  beta-worker:\n    image: nginx\n","marquee_id":"1","create_playground":true,"persist_volumes":false,"service_subdomains":{"alpha":"alpha-demo","beta-worker":"worker-demo"}}`
	decision := New().Check(newGateRequest(t, http.MethodPost, "/api/launches", body))
	if !decision.Allowed {
		t.Fatalf("launch service_subdomains should be allowed: %#v", decision)
	}
}

func TestCheckRejectsRepositoryOnlyLaunch(t *testing.T) {
	body := `{"name":"pg","repository_url":"https://github.com/acme/demo"}`
	decision := New().Check(newGateRequest(t, http.MethodPost, "/api/launches", body))
	if decision.Allowed {
		t.Fatal("repository-only launch should not be allowed")
	}
	if !containsString(decision.Details["unsupported"].([]string), "field:repository_url") {
		t.Fatalf("unsupported details = %#v", decision.Details["unsupported"])
	}
}

func TestCheckRejectsRepositoryConfigLaunchFieldsWithSpecificReasons(t *testing.T) {
	body := `{"name":"pg","repository_url":"https://github.com/acme/demo","config_path":"deploy/fibe.yml","github_ref":"main"}`
	decision := New().Check(newGateRequest(t, http.MethodPost, "/api/launches", body))
	if decision.Allowed {
		t.Fatal("repository config launch should not be allowed")
	}
	for _, want := range []string{"field:config_path", "field:github_ref", "field:repository_url"} {
		if !containsString(decision.Details["unsupported"].([]string), want) {
			t.Fatalf("unsupported details = %#v, want %q", decision.Details["unsupported"], want)
		}
	}
	reason := decision.Details["reason"].(string)
	if !strings.Contains(reason, "caller-supplied compose_yaml") {
		t.Fatalf("reason = %q, want caller-supplied compose guidance", reason)
	}
}

func TestCheckRejectsLegacyLaunchSubdomainAliases(t *testing.T) {
	body := `{"name":"pg","compose_yaml":"services:\n  alpha:\n    image: nginx\n","service_subdomains":{"alpha":"alpha-demo"},"apiSubdomain":"api-demo","frontendSubdomain":"web-demo"}`
	decision := New().Check(newGateRequest(t, http.MethodPost, "/api/launches", body))
	if decision.Allowed {
		t.Fatal("legacy launch subdomain aliases should be rejected")
	}
	for _, want := range []string{"field:apiSubdomain", "field:frontendSubdomain"} {
		if !containsString(decision.Details["unsupported"].([]string), want) {
			t.Fatalf("unsupported details = %#v, want %q", decision.Details["unsupported"], want)
		}
	}
	reason := decision.Details["reason"].(string)
	if !strings.Contains(reason, "service_subdomains") {
		t.Fatalf("reason = %q, want service_subdomains guidance", reason)
	}
}

func TestCheckRejectsPersistentVolumesAsStateless(t *testing.T) {
	rejected := New().Check(newGateRequest(t, http.MethodPost, "/api/playspecs", `{"playspec":{"name":"stateful","persist_volumes":true}}`))
	if rejected.Allowed {
		t.Fatal("persist_volumes=true should be rejected")
	}
	if !containsString(rejected.Details["unsupported"].([]string), "field:persist_volumes") {
		t.Fatalf("unsupported details = %#v", rejected.Details["unsupported"])
	}
	rejectedNumeric := New().Check(newGateRequest(t, http.MethodPost, "/api/playspecs", `{"playspec":{"name":"stateful","persist_volumes":1}}`))
	if rejectedNumeric.Allowed {
		t.Fatal("numeric truthy persist_volumes should be rejected")
	}
	if !containsString(rejectedNumeric.Details["unsupported"].([]string), "field:persist_volumes") {
		t.Fatalf("unsupported details = %#v", rejectedNumeric.Details["unsupported"])
	}
	rejectedLaunchNumeric := New().Check(newGateRequest(t, http.MethodPost, "/api/launches", `{"name":"stateful","compose_yaml":"services:\n  web:\n    image: nginx\n","persist_volumes":1}`))
	if rejectedLaunchNumeric.Allowed {
		t.Fatal("numeric truthy launch persist_volumes should be rejected")
	}
	if !containsString(rejectedLaunchNumeric.Details["unsupported"].([]string), "field:persist_volumes") {
		t.Fatalf("unsupported details = %#v", rejectedLaunchNumeric.Details["unsupported"])
	}
	textPlain := newGateRequest(t, http.MethodPost, "/api/playspecs", `{"playspec":{"name":"stateful","persist_volumes":true}}`)
	textPlain.Header.Set("Content-Type", "text/plain")
	rejectedTextPlain := New().Check(textPlain)
	if rejectedTextPlain.Allowed {
		t.Fatal("persist_volumes=true should be rejected regardless of Content-Type")
	}
	if !containsString(rejectedTextPlain.Details["unsupported"].([]string), "field:persist_volumes") {
		t.Fatalf("unsupported details = %#v", rejectedTextPlain.Details["unsupported"])
	}
	allowed := New().Check(newGateRequest(t, http.MethodPost, "/api/playspecs", `{"playspec":{"name":"stateless","persist_volumes":false}}`))
	if !allowed.Allowed {
		t.Fatalf("persist_volumes=false should be allowed: %#v", allowed)
	}
}

func TestCheckRejectsUnsupportedComposeFeatures(t *testing.T) {
	for _, tc := range []struct {
		body string
		want string
	}{
		{
			body: `{"compose_yaml":"services:\n  web:\n    image: nginx\n    labels:\n      fibe.gg/zerodowntime: \"true\"\n"}`,
			want: "field:compose_yaml.services.web.labels.fibe.gg/zerodowntime",
		},
		{
			body: `{"compose_yaml":"services:\n  web:\n    image: nginx\n    labels:\n      - fibe.gg/job_watch\n"}`,
			want: "field:compose_yaml.services.web.labels.fibe.gg/job_watch",
		},
		{
			body: `{"compose_yaml":"services:\n  web:\n    image: nginx\n    labels:\n      fibe.gg/env_file: config/app.env\n"}`,
			want: "field:compose_yaml.services.web.labels.fibe.gg/env_file",
		},
		{
			body: `{"compose_yaml":"services:\n  web:\n    image: nginx\n    labels:\n      fibe.gg/env_file:\n        path: config/app.env\n"}`,
			want: "field:compose_yaml.services.web.labels.fibe.gg/env_file",
		},
		{
			body: `{"compose_yaml":"services:\n  web:\n    image: nginx\n    env_file:\n      - config/app.env\n"}`,
			want: "field:compose_yaml.services.web.env_file",
		},
		{
			body: `{"compose_yaml":"services:\n  web:\n    image: nginx\n    labels:\n      \" fibe.gg/job_watch \": \"true\"\n"}`,
			want: "field:compose_yaml.services.web.labels.fibe.gg/job_watch",
		},
		{
			body: `{"compose_yaml":"x-fibe.gg:\n  job_mode: true\nservices:\n  web:\n    image: nginx\n"}`,
			want: "field:compose_yaml.x-fibe.gg.job_mode",
		},
		{
			body: `{"compose_yaml":"x-fibe.gg:\n  metadata:\n    job_mode: true\nservices:\n  web:\n    image: nginx\n"}`,
			want: "field:compose_yaml.x-fibe.gg.metadata.job_mode",
		},
		{
			body: `{"compose_yaml":"x-fibe.gg:\n  metadata:\n    preserve_ports: true\nservices:\n  web:\n    image: nginx\n"}`,
			want: "field:compose_yaml.x-fibe.gg.metadata.preserve_ports",
		},
		{
			body: `{"compose_yaml":"x-fibe.gg:\n  metadata:\n    source_defaults: true\nservices:\n  web:\n    image: nginx\n"}`,
			want: "field:compose_yaml.x-fibe.gg.metadata.source_defaults",
		},
	} {
		decision := New().Check(newGateRequest(t, http.MethodPost, "/api/launches", tc.body))
		if decision.Allowed {
			t.Fatalf("unsupported compose feature should not be allowed: %s", tc.body)
		}
		unsupported := decision.Details["unsupported"].([]string)
		if len(unsupported) == 0 || !containsString(unsupported, tc.want) {
			t.Fatalf("unsupported details = %#v", unsupported)
		}
	}
}

func TestCheckRejectsUnsupportedPlaygroundActions(t *testing.T) {
	for _, body := range []string{
		`{"action_type":"enable_maintenance"}`,
		`{"action_type":"rollout","build_overrides_yaml":{"web":{}}}`,
		`{"action_type":"rollout","playground":{}}`,
		`{"action_type":"rollout","playground":null}`,
		`{"action_type":"template_switch"}`,
	} {
		decision := New().Check(newGateRequest(t, http.MethodPost, "/api/playgrounds/demo/operations", body))
		if decision.Allowed {
			t.Fatalf("unsupported playground operation should not be allowed: %s", body)
		}
		supported, ok := decision.Details["supported"].([]string)
		if !ok || !containsString(supported, "rollout") || !containsString(supported, "hard_restart") {
			t.Fatalf("expected supported playground action detail: %#v", decision.Details)
		}
	}
}

func TestCheckRejectsPlaygroundActionAlias(t *testing.T) {
	decision := New().Check(newGateRequest(t, http.MethodPost, "/api/playgrounds/demo/operations", `{"action":"rollout"}`))
	if decision.Allowed {
		t.Fatal("legacy playground operation action alias should not be allowed")
	}
	unsupported, ok := decision.Details["unsupported"].([]string)
	if !ok || !containsString(unsupported, "field:action") {
		t.Fatalf("unsupported details = %#v", decision.Details)
	}
}

func TestCheckPreservesInvalidJSONForHandlers(t *testing.T) {
	for _, body := range []string{
		`{"playground":`,
		`{"playground":{"build_overrides_yaml":{"web":{}}}} {}`,
	} {
		req := newGateRequest(t, http.MethodPost, "/api/playgrounds", body)
		decision := New().Check(req)
		if !decision.Allowed {
			t.Fatalf("invalid JSON should pass through to handler validation: %#v", decision)
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read restored body: %v", err)
		}
		if string(raw) != body {
			t.Fatalf("body was not restored:\n got %q\nwant %q", raw, body)
		}
	}
}

func TestCheckRejectsOversizedInspectableBody(t *testing.T) {
	body := `{"prop":{"name":"p","repository_url":"https://github.com/acme/p.git"}}`
	req := newGateRequest(t, http.MethodPost, "/api/props", body)
	req.ContentLength = maxJSONBodyBytes + 1

	decision := New().Check(req)
	if decision.Allowed {
		t.Fatal("oversized mutating API body should be rejected before handlers")
	}
	if decision.Status != http.StatusRequestEntityTooLarge || decision.Code != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("unexpected oversized decision: %#v", decision)
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(raw) != body {
		t.Fatalf("body was unexpectedly consumed:\n got %q\nwant %q", raw, body)
	}
}

func newGateRequest(t *testing.T, method string, path string, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://fibe-distilled.test"+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func assertGateAllowed(t *testing.T, method string, path string, body string) Decision {
	t.Helper()
	decision := New().Check(newGateRequest(t, method, path, body))
	if !decision.Allowed {
		t.Fatalf("%s %s should be allowed: %#v", method, path, decision)
	}
	return decision
}

func assertGateUnsupported(t *testing.T, method string, path string, body string, wants ...string) Decision {
	t.Helper()
	decision := New().Check(newGateRequest(t, method, path, body))
	if decision.Allowed {
		t.Fatalf("%s %s should not be allowed", method, path)
	}
	unsupported := decision.Details["unsupported"].([]string)
	for _, want := range wants {
		if !containsString(unsupported, want) {
			t.Fatalf("unsupported details = %#v, want %q", unsupported, want)
		}
	}
	return decision
}

func assertDecisionReasonContains(t *testing.T, decision Decision, want string) {
	t.Helper()
	if reason := decision.Details["reason"].(string); !strings.Contains(reason, want) {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type trackingReadCloser struct {
	*strings.Reader
	closed bool
}

func (b *trackingReadCloser) Close() error {
	b.closed = true
	return nil
}

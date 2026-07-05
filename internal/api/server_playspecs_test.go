package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	"gopkg.in/yaml.v3"
)

func TestPlayspecPlaygroundLifecycle(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_RESOLVE_COMMIT": {Stdout: "abcdef1234567890abcdef1234567890abcdef12\n"},
		},
	}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": " demo ",
		"base_compose_yaml": `services:
  web:
    build: .
    labels:
      fibe.gg/repo_url: https://example.com/acme/demo.git
      fibe.gg/port: "80"
      fibe.gg/subdomain: web
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)
	if playspec["name"] != "demo" {
		t.Fatalf("unexpected playspec: %#v", playspec)
	}

	pgBody := map[string]any{"playground": map[string]any{"name": " demo-pg ", "playspec_id": int64(playspec["id"].(float64))}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)
	if pg["name"] != "demo-pg" {
		t.Fatalf("playground name should be trimmed, got %#v", pg)
	}
	if _, ok := pg["maintenance_enabled"]; ok {
		t.Fatalf("playground should not expose removed maintenance response hint: %#v", pg)
	}
	waitForPlaygroundStatus(t, st, "demo-pg", domain.StatusRunning)

	var status map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/playgrounds/demo-pg/status", nil, "test-token")
	decodeResp(t, res, &status)
	if status["status"] != "running" {
		t.Fatalf("expected status running, got %#v", status)
	}
	services := status["services"].([]any)
	if len(services) != 1 {
		t.Fatalf("expected service readiness, got %#v", status)
	}
	builds := status["build_statuses"].([]any)
	if len(builds) != 1 {
		t.Fatalf("expected dynamic build status, got %#v", status)
	}
}

func TestPlayspecServiceMetadataAppliesToStoredCompose(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	body := map[string]any{"playspec": map[string]any{
		"name": "service-metadata-spec",
		"base_compose_yaml": `services:
  web:
    image: nginx:alpine
  worker:
    image: alpine
`,
		"services": []map[string]any{
			{
				"name":  "web",
				"type":  "static",
				"image": "nginx:latest",
				"exposure": map[string]any{
					"enabled":    true,
					"port":       8080,
					"subdomain":  "app",
					"visibility": "external",
				},
			},
		},
	}}
	var created map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", body, "test-token")
	decodeResp(t, res, &created)

	stored, err := st.GetPlayspec(context.Background(), "service-metadata-spec")
	if err != nil {
		t.Fatalf("get stored playspec: %v", err)
	}
	for _, want := range []string{
		"image: nginx:latest",
		"fibe.gg/port: \"8080\"",
		"fibe.gg/subdomain: app",
		"fibe.gg/visibility: external",
	} {
		if !strings.Contains(stored.BaseComposeYAML, want) {
			t.Fatalf("expected %q in stored compose:\n%s", want, stored.BaseComposeYAML)
		}
	}
}

func TestPlayspecServiceMetadataRejectsIgnoredServiceDefinitions(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	for _, tt := range []struct {
		name     string
		services []any
	}{
		{
			name:     "unknown-service",
			services: []any{map[string]any{"name": "missing", "type": "static"}},
		},
		{
			name: "duplicate-service",
			services: []any{
				map[string]any{"name": "web", "type": "static"},
				map[string]any{"name": "web", "type": "static"},
			},
		},
		{
			name:     "invalid-type",
			services: []any{map[string]any{"name": "web", "type": "invalid"}},
		},
		{
			name:     "blank-type",
			services: []any{map[string]any{"name": "web", "type": " "}},
		},
		{
			name:     "null-type",
			services: []any{map[string]any{"name": "web", "type": nil}},
		},
		{
			name:     "numeric-type",
			services: []any{map[string]any{"name": "web", "type": 123}},
		},
		{
			name:     "non-object",
			services: []any{"web"},
		},
		{
			name:     "blank-supported-metadata",
			services: []any{map[string]any{"name": "web", "type": "static", "image": " "}},
		},
		{
			name:     "wrong-supported-metadata-type",
			services: []any{map[string]any{"name": "web", "type": "static", "image": 123}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{"playspec": map[string]any{
				"name":              "bad-" + tt.name,
				"base_compose_yaml": "services:\n  web:\n    image: nginx\n",
				"services":          tt.services,
			}}
			res := doReq(t, srv, http.MethodPost, "/api/playspecs", body, "test-token")
			defer closeResponseBody(t, res)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("invalid services should fail handler validation, got %d", res.StatusCode)
			}
		})
	}
}

func TestPlayspecCreateRejectsInvalidTemplateVariables(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := map[string]any{"playspec": map[string]any{
		"name": "invalid-template-vars",
		"base_compose_yaml": `x-fibe.gg:
  variables:
    TOKEN:
      name: Token
      validation: "/[[/"
      path: services.alpha.environment.TOKEN
services:
  alpha:
    image: alpine
    environment: {}
`,
	}}

	res := doReq(t, srv, http.MethodPost, "/api/playspecs", body, "test-token")
	assertErrorMessageContains(t, res, http.StatusBadRequest, "BAD_REQUEST", `variable "TOKEN" validation regex is invalid`)
}

func TestPlayspecPatchWithEmptyServicesDerivesSummaries(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	var created map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", map[string]any{"playspec": map[string]any{
		"name":              "empty-services-patch",
		"base_compose_yaml": "services:\n  web:\n    image: nginx\n",
	}}, "test-token")
	decodeResp(t, res, &created)

	var updated map[string]any
	res = doReq(t, srv, http.MethodPatch, "/api/playspecs/empty-services-patch", map[string]any{"playspec": map[string]any{
		"base_compose_yaml": "services:\n  api:\n    image: alpine\n",
		"services":          []any{},
	}}, "test-token")
	decodeResp(t, res, &updated)

	services := updated["services"].([]any)
	if len(services) != 1 || services[0].(map[string]any)["name"] != "api" {
		t.Fatalf("empty services patch should derive summaries from updated compose, got %#v", updated)
	}
}

func TestPlayspecUpdateSavesFromCurrentRow(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "current-row-spec",
		BaseComposeYAML: "services:\n  web:\n    image: alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	description := "current description"
	current := ps
	current.Description = &description
	if _, err := st.SavePlayspec(ctx, current); err != nil {
		t.Fatalf("save current playspec: %v", err)
	}

	var updated domain.Playspec
	res := doReq(t, srv, http.MethodPatch, "/api/playspecs/current-row-spec", map[string]any{
		"playspec": map[string]any{"name": "renamed-current-row-spec"},
	}, "test-token")
	decodeResp(t, res, &updated)
	if updated.Name != "renamed-current-row-spec" {
		t.Fatalf("update should apply requested name, got %#v", updated)
	}
	if updated.Description == nil || *updated.Description != description {
		t.Fatalf("update should preserve current description, got %#v", updated.Description)
	}
}

func TestPersistentVolumePlayspecRejectedAsStateless(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":            "stateful",
		"persist_volumes": true,
		"base_compose_yaml": `services:
  db:
    image: postgres:17
`,
	}}
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("persist_volumes=true should be rejected as unsupported, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)

	statelessBody := map[string]any{"playspec": map[string]any{
		"name": "stateless",
		"base_compose_yaml": `services:
  db:
    image: postgres:17
`,
	}}
	var playspec map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playspecs", statelessBody, "test-token")
	decodeResp(t, res, &playspec)
	if playspec["persist_volumes"] != false {
		t.Fatalf("playspec should default to stateless, got %#v", playspec["persist_volumes"])
	}
	if _, ok := playspec["trigger_enabled"]; ok {
		t.Fatalf("playspec should not expose removed trigger response hints: %#v", playspec)
	}
	if _, ok := playspec["muti_mode"]; ok {
		t.Fatalf("playspec should not expose removed muti response hints: %#v", playspec)
	}
}

func TestPlayspecDynamicServiceMetadataRoundTripsThroughPatch(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "dynamic-roundtrip",
		"base_compose_yaml": `services:
  runner:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/service.git
      fibe.gg/dockerfile: deploy/Dockerfile
      fibe.gg/start_command: npm ci && npm test
      fibe.gg/build_target: runner
      fibe.gg/build_args: NODE_VERSION=22,APP_ENV=test
      fibe.gg/production: "true"
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	services := playspec["services"].([]any)
	service := services[0].(map[string]any)
	if service["production"] != true {
		t.Fatalf("dynamic service should expose production mode metadata for round-trip, got %#v", service)
	}
	patchBody := map[string]any{"playspec": map[string]any{"services": services}}
	res = doReq(t, srv, http.MethodPatch, "/api/playspecs/dynamic-roundtrip", patchBody, "test-token")
	decodeResp(t, res, &playspec)

	stored, err := st.GetPlayspec(context.Background(), "dynamic-roundtrip")
	if err != nil {
		t.Fatalf("get stored playspec: %v", err)
	}
	composeYAML := stored.BaseComposeYAML
	for _, want := range []string{
		"fibe.gg/repo_url: https://github.com/acme/service.git",
		"fibe.gg/dockerfile: deploy/Dockerfile",
		"fibe.gg/start_command: npm ci && npm test",
		"fibe.gg/build_target: runner",
		"fibe.gg/build_args: NODE_VERSION=22,APP_ENV=test",
		"fibe.gg/production: \"true\"",
	} {
		if !strings.Contains(composeYAML, want) {
			t.Fatalf("expected %q in round-tripped compose:\n%s", want, composeYAML)
		}
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		t.Fatalf("parse compose: %v\n%s", err, composeYAML)
	}
	runner := doc["services"].(map[string]any)["runner"].(map[string]any)
	command := runner["command"].([]any)
	if len(command) != 3 || command[0] != "sh" || command[1] != "-c" || command[2] != "npm ci && npm test" {
		t.Fatalf("expected start_command to survive as service command, got %#v\n%s", command, composeYAML)
	}
}

func TestPlayspecDynamicServiceWithoutEnvFileDoesNotInventMetadata(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "dynamic-no-env-file",
		"base_compose_yaml": `services:
  api:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/api.git
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)
	services := playspec["services"].([]any)
	service := services[0].(map[string]any)
	if service["env_file_path"] != nil {
		t.Fatalf("env_file_path should not be exposed by fibe-distilled, got %#v", service)
	}

	res = doReq(t, srv, http.MethodGet, "/api/playspecs/dynamic-no-env-file/services", nil, "test-token")
	var serviceBody []any
	decodeResp(t, res, &serviceBody)
	service = serviceBody[0].(map[string]any)
	if service["env_file_path"] != nil {
		t.Fatalf("services endpoint should not expose env_file_path: %#v", service)
	}
}

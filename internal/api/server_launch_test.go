package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"gopkg.in/yaml.v3"
)

func TestLaunchRejectsJobMode(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := map[string]any{
		"name":     "job-demo",
		"job_mode": true,
		"compose_yaml": `services:
  check:
    image: alpine
`,
	}
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	assertErrorCode(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestLaunchRejectsTargetType(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := map[string]any{
		"name":        "target-type-demo",
		"target_type": "trick",
		"compose_yaml": `services:
  check:
    image: alpine
`,
	}
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	assertErrorCode(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestLaunchRejectsRepositoryURLWithoutComposeYAML(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":           "repo-only-launch",
		"repository_url": "https://github.com/acme/demo.git",
	}, "test-token")
	assertErrorCode(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestLaunchRejectsComposeWithoutName(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"compose_yaml": `services:
  alpha:
    image: nginx:alpine
`,
	}, "test-token")
	assertBadRequest(t, res, "compose launch without name")
}

func TestLaunchCompilesRootDomainFromSelectedMarquee(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	domains := "apps.example.test, other.example.test"
	marquee := ensureTestConfiguredMarqueeWith(t, st, domain.Marquee{DomainsInput: &domains})
	body := map[string]any{
		"name":              "root-domain-launch",
		"marquee_id":        marquee.ID,
		"create_playground": false,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
    environment:
      PUBLIC_URL: https://app.$$root_domain
`,
	}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	decodeResp(t, res, &launch)
	playspecID := numberID(launch["playspec_id"])

	playspec, err := st.GetPlayspec(context.Background(), playspecID)
	if err != nil {
		t.Fatalf("get playspec: %v", err)
	}
	if !strings.Contains(playspec.BaseComposeYAML, "PUBLIC_URL: https://app.apps.example.test") {
		t.Fatalf("expected launch to compile root domain from selected marquee:\n%s", playspec.BaseComposeYAML)
	}
}

func TestLaunchWithoutPlaygroundReturnsNullPlaygroundID(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "playspec-only-launch",
		"create_playground": false,
		"compose_yaml": "services:\n" +
			"  web:\n" +
			"    image: nginx:alpine\n",
	}, "test-token")
	decodeResp(t, res, &launch)
	if launch["playspec_id"] == nil {
		t.Fatalf("playspec-only launch should still return playspec_id: %#v", launch)
	}
	if value, ok := launch["playground_id"]; !ok || value != nil {
		t.Fatalf("playspec-only launch should return playground_id:null, got %#v", launch)
	}
	propsCreated, ok := launch["props_created"].([]any)
	if !ok || len(propsCreated) != 0 {
		t.Fatalf("playspec-only launch should return props_created:[], got %#v", launch)
	}
}

func TestLaunchAcceptsScalarTemplateVariables(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	body := map[string]any{
		"name":              "scalar-variable-launch",
		"create_playground": false,
		"variables": map[string]any{
			"PORT":  8080,
			"DEBUG": true,
			"TAG":   1.25,
		},
		"compose_yaml": `x-fibe.gg:
  variables:
    PORT:
      name: Port
      path: services.web.environment.PORT
    DEBUG:
      name: Debug
      path: services.web.environment.DEBUG
    TAG:
      name: Tag
services:
  web:
    image: nginx:$$var__TAG
    environment: {}
`,
	}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	decodeResp(t, res, &launch)
	playspecID := numberID(launch["playspec_id"])

	playspec, err := st.GetPlayspec(context.Background(), playspecID)
	if err != nil {
		t.Fatalf("get playspec: %v", err)
	}
	var rendered map[string]any
	if err := yaml.Unmarshal([]byte(playspec.BaseComposeYAML), &rendered); err != nil {
		t.Fatalf("parse rendered compose: %v\n%s", err, playspec.BaseComposeYAML)
	}
	services := rendered["services"].(map[string]any)
	web := services["web"].(map[string]any)
	env := web["environment"].(map[string]any)
	if env["PORT"] != 8080 || env["DEBUG"] != true || web["image"] != "nginx:1.25" {
		t.Fatalf("scalar variables were not compiled correctly: image=%#v env=%#v\n%s", web["image"], env, playspec.BaseComposeYAML)
	}
}

func TestLaunchAcceptsScalarEnvOverrides(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	body := map[string]any{
		"name": "scalar-env-launch",
		"env_overrides": map[string]any{
			"PORT":  8080,
			"DEBUG": true,
			"TAG":   1.25,
			"EMPTY": "",
		},
		"compose_yaml": "services:\n  web:\n    image: alpine\n",
	}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	decodeResp(t, res, &launch)

	stored, err := st.GetPlayground(context.Background(), numberID(launch["playground_id"]))
	if err != nil {
		t.Fatalf("get launch playground: %v", err)
	}
	want := map[string]string{"PORT": "8080", "DEBUG": "true", "TAG": "1.25", "EMPTY": ""}
	if !equalStringMaps(stored.EnvOverrides, want) {
		t.Fatalf("env_overrides not normalized: %#v", stored.EnvOverrides)
	}
	assertRenderedEnv(t, stored.GeneratedComposeYAML, "web", want)
}

func TestLaunchRejectsTemplateRepositoryShorthandAfterCompilation(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "bad-template-repo",
		"create_playground": false,
		"variables":         map[string]any{"REPO": "acme/demo"},
		"compose_yaml": `x-fibe.gg:
  variables:
    REPO:
      name: Repository
      required: true
services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: $$var__REPO
`,
	}, "test-token")
	assertBadRequest(t, res, "compiled repository shorthand")
}

func TestLaunchRejectsExplicitBlankAndNullFields(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	validCompose := "services:\n  web:\n    image: alpine\n"
	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "blank compose", body: map[string]any{"compose_yaml": "   "}},
		{name: "blank name", body: map[string]any{"name": "", "compose_yaml": validCompose}},
		{name: "blank repository", body: map[string]any{"repository_url": "", "compose_yaml": validCompose}},
		{name: "repository shorthand", body: map[string]any{"name": "shorthand", "repository_url": "acme/demo", "compose_yaml": validCompose}},
		{name: "blank marquee", body: map[string]any{"marquee_id": "", "compose_yaml": validCompose}},
		{name: "zero marquee id", body: map[string]any{"marquee_id": 0, "compose_yaml": validCompose}},
		{name: "negative marquee id", body: map[string]any{"marquee_id": -1, "compose_yaml": validCompose}},
		{name: "null create playground", body: map[string]any{"create_playground": nil, "compose_yaml": validCompose}},
		{name: "null env overrides", body: map[string]any{"env_overrides": nil, "compose_yaml": validCompose}},
		{name: "empty env overrides", body: map[string]any{"env_overrides": map[string]any{}, "compose_yaml": validCompose}},
		{name: "blank env override key", body: map[string]any{"env_overrides": map[string]any{" ": "value"}, "compose_yaml": validCompose}},
		{name: "null env override value", body: map[string]any{"env_overrides": map[string]any{"PORT": nil}, "compose_yaml": validCompose}},
		{name: "array env override value", body: map[string]any{"env_overrides": map[string]any{"PORT": []any{8080}}, "compose_yaml": validCompose}},
		{name: "object env override value", body: map[string]any{"env_overrides": map[string]any{"PORT": map[string]any{"value": 8080}}, "compose_yaml": validCompose}},
		{name: "empty variables", body: map[string]any{"variables": map[string]any{}, "compose_yaml": validCompose}},
		{name: "blank variable key", body: map[string]any{"variables": map[string]any{" ": "value"}, "compose_yaml": validCompose}},
		{name: "null variables", body: map[string]any{"variables": nil, "compose_yaml": validCompose}},
		{name: "null variable value", body: map[string]any{"variables": map[string]any{"PORT": nil}, "compose_yaml": validCompose}},
		{name: "array variable value", body: map[string]any{"variables": map[string]any{"PORT": []any{8080}}, "compose_yaml": validCompose}},
		{name: "object variable value", body: map[string]any{"variables": map[string]any{"PORT": map[string]any{"value": 8080}}, "compose_yaml": validCompose}},
		{name: "null service subdomains", body: map[string]any{"service_subdomains": nil, "compose_yaml": validCompose}},
		{name: "empty service subdomains", body: map[string]any{"service_subdomains": map[string]any{}, "compose_yaml": validCompose}},
		{name: "blank service subdomain value", body: map[string]any{"service_subdomains": map[string]any{"alpha": ""}, "compose_yaml": validCompose}},
		{name: "null services", body: map[string]any{"services": nil, "compose_yaml": validCompose}},
		{name: "empty services", body: map[string]any{"services": map[string]any{}, "compose_yaml": validCompose}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res := doReq(t, srv, http.MethodPost, "/api/launches", tt.body, "test-token")
			assertBadRequest(t, res, "bad launch field")
		})
	}
}

func TestLaunchRejectsRuntimeOnlyFieldsWhenNotCreatingPlayground(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	base := map[string]any{
		"name":              "playspec-only-runtime-fields",
		"create_playground": false,
		"compose_yaml": `services:
  alpha:
    image: nginx:alpine
`,
	}
	for _, tt := range []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{
			name:  "service auth password",
			extra: map[string]any{"services": map[string]any{"alpha": map[string]any{"auth_password": "secret"}}},
			want:  "field:services.alpha.auth_password",
		},
		{
			name:  "env overrides",
			extra: map[string]any{"env_overrides": map[string]any{"RUNTIME_ONLY": "1"}},
			want:  "field:env_overrides",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := launchBodyWith(base, tt.extra)
			res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
			assertBadRequestField(t, res, tt.want)
		})
	}

	body := launchBodyWith(base, nil)
	body["name"] = "playspec-only-compose-service-override"
	body["services"] = map[string]any{"alpha": map[string]any{
		"env_vars":        map[string]any{"COMPOSE_LEVEL": "1"},
		"repo_url":        "ssh://git.example.test/acme/launch-service.git",
		"dockerfile_path": "deploy/Dockerfile",
		"build_target":    "runner",
		"build_args":      "NODE_VERSION=22",
	}}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	decodeResp(t, res, &launch)
	playspec, err := st.GetPlayspec(context.Background(), numberID(launch["playspec_id"]))
	if err != nil {
		t.Fatalf("get playspec: %v", err)
	}
	for _, want := range []string{
		"COMPOSE_LEVEL: \"1\"",
		"fibe.gg/repo_url: ssh://git.example.test/acme/launch-service.git",
		"fibe.gg/dockerfile: deploy/Dockerfile",
		"fibe.gg/build_target: runner",
		"fibe.gg/build_args: NODE_VERSION=22",
	} {
		if !strings.Contains(playspec.BaseComposeYAML, want) {
			t.Fatalf("playspec compose missing %q:\n%s", want, playspec.BaseComposeYAML)
		}
	}
}

func TestLaunchChecksWritableReposIntroducedByServiceOverrides(t *testing.T) {
	githubBaseURL := startNonWritableGitHubRepoFixture(t, "acme/locked-demo")

	srv, st := newTestServerWithStoreAndGitHubBaseURL(t, nil, githubBaseURL)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "service-override-repo-access",
		"create_playground": false,
		"compose_yaml": `services:
  alpha:
    image: nginx:alpine
`,
		"services": map[string]any{
			"alpha": map[string]any{"repo_url": "https://github.com/acme/locked-demo.git"},
		},
	}, "test-token")
	if res.StatusCode != http.StatusUnprocessableEntity {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("service override repo should be checked for write access, got %d: %#v", res.StatusCode, got)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode repo access error: %v", err)
	}
	closeResponseBody(t, res)
	if got["error"].(map[string]any)["code"] != "REPOSITORY_REQUIRES_FORK" {
		t.Fatalf("unexpected repo access error: %#v", got)
	}
	if _, err := st.GetPlayspec(context.Background(), "service-override-repo-access"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("launch should fail before creating resources, got err=%v", err)
	}
}

func TestLaunchServiceSubdomainsApplyByExplicitServiceName(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	body := map[string]any{
		"name":              "explicit-subdomain-launch",
		"create_playground": false,
		"service_subdomains": map[string]string{
			"alpha":  "alpha-route",
			"events": "events",
		},
		"compose_yaml": `services:
  alpha:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
  events:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
`,
	}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	decodeResp(t, res, &launch)

	playspec, err := st.GetPlayspec(context.Background(), numberID(launch["playspec_id"]))
	if err != nil {
		t.Fatalf("get playspec: %v", err)
	}
	for _, want := range []string{
		"fibe.gg/subdomain: alpha-route",
		"fibe.gg/subdomain: events",
	} {
		if !strings.Contains(playspec.BaseComposeYAML, want) {
			t.Fatalf("expected explicit service_subdomains to compile %q into compose:\n%s", want, playspec.BaseComposeYAML)
		}
	}
}

func TestLaunchRejectsAmbiguousTrimmedServiceSubdomains(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "ambiguous-subdomain-launch",
		"create_playground": false,
		"service_subdomains": map[string]string{
			"alpha":  "alpha-route",
			" alpha": "other-route",
		},
		"compose_yaml": `services:
  alpha:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
`,
	}, "test-token")
	if res.StatusCode != http.StatusBadRequest {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected ambiguous service_subdomains to fail, got %d: %#v", res.StatusCode, got)
	}
	closeResponseBody(t, res)
}

func TestLaunchRejectsUnknownServiceOverrideNames(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	base := map[string]any{
		"name":              "unknown-service-launch",
		"create_playground": false,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
`,
	}
	body := map[string]any{}
	for key, value := range base {
		body[key] = value
	}
	body["service_subdomains"] = map[string]string{"missing-service": "missing-demo"}
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown service_subdomains key should return 400, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)

	body = map[string]any{}
	for key, value := range base {
		body[key] = value
	}
	body["services"] = map[string]any{"missing-service": map[string]any{"subdomain": "missing-demo"}}
	res = doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown service override key should return 400, got %d", res.StatusCode)
	}

	body = map[string]any{}
	for key, value := range base {
		body[key] = value
	}
	body["services"] = map[string]any{"web": "not-an-object"}
	res = doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed service override should return 400, got %d", res.StatusCode)
	}
}

func TestLaunchResolvesMarqueeByName(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	domains := "named.example.test"
	ensureTestConfiguredMarqueeWith(t, st, domain.Marquee{DomainsInput: &domains})
	body := map[string]any{
		"name":              "named-marquee-launch",
		"marquee_id":        store.ConfiguredMarqueeName,
		"create_playground": false,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
    environment:
      PUBLIC_URL: https://app.$$root_domain
`,
	}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	decodeResp(t, res, &launch)

	playspec, err := st.GetPlayspec(context.Background(), numberID(launch["playspec_id"]))
	if err != nil {
		t.Fatalf("get playspec: %v", err)
	}
	if !strings.Contains(playspec.BaseComposeYAML, "PUBLIC_URL: https://app.named.example.test") {
		t.Fatalf("expected launch to resolve marquee name before root-domain compile:\n%s", playspec.BaseComposeYAML)
	}
}

func TestLaunchRepositoryPropCreationFailureCleansUpPlayspec(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	srv, st := newTestServerWithDBPath(t, dbPath, nil)
	defer srv.Close()

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer closeSQLDB(t, rawDB)
	if _, err := rawDB.ExecContext(context.Background(), "PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("set busy timeout: %v", err)
	}
	if _, err := rawDB.ExecContext(context.Background(), "ALTER TABLE props RENAME TO props_broken"); err != nil {
		t.Fatalf("break props table: %v", err)
	}

	body := map[string]any{
		"name":              "prop-write-fails",
		"repository_url":    "ssh://git.example.test/acme/demo.git",
		"create_playground": false,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
`,
	}
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	if res.StatusCode != http.StatusInternalServerError {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected prop persistence failure to return 500, got %d: %#v", res.StatusCode, got)
	}
	closeResponseBody(t, res)

	if _, err := st.GetPlayspec(context.Background(), "prop-write-fails"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("launch-created playspec should be cleaned up after prop persistence failure, got %v", err)
	}
}

func TestLaunchRepositoryPropDuplicateSameRepoIsTolerated(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	if _, err := st.CreateProp(context.Background(), domain.Prop{Name: "existing-repo", RepositoryURL: "ssh://git.example.test/acme/demo.git"}); err != nil {
		t.Fatalf("create existing prop: %v", err)
	}
	body := map[string]any{
		"name":              " existing-repo ",
		"repository_url":    " ssh://git.example.test/acme/demo ",
		"create_playground": false,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
`,
	}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	decodeResp(t, res, &launch)
	if _, err := st.GetPlayspec(context.Background(), "existing-repo"); err != nil {
		t.Fatalf("expected playspec to exist after same-repo launch: %v", err)
	}
	if _, err := st.GetPlayspec(context.Background(), " existing-repo "); err == nil {
		t.Fatal("playspec lookup by untrimmed launch name should not create a separate raw-name row")
	}
	if created, ok := launch["props_created"].([]any); ok && len(created) != 0 {
		t.Fatalf("same-repo duplicate prop should not report a newly created prop: %#v", launch)
	}
	source, ok := launch["source"].(map[string]any)
	if !ok || source["repository_url"] != "ssh://git.example.test/acme/demo" {
		t.Fatalf("launch source repository_url should be trimmed, got %#v", launch["source"])
	}
}

func TestLaunchDuplicateSameComposeReusesExistingPlayspec(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	composeYAML := `services:
  web:
    image: nginx:alpine
`
	firstBody := map[string]any{
		"name":              "replayed-launch",
		"create_playground": false,
		"compose_yaml":      composeYAML,
	}
	var first map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", firstBody, "test-token")
	decodeResp(t, res, &first)

	secondBody := map[string]any{
		"name":         "replayed-launch",
		"compose_yaml": composeYAML,
	}
	var second map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/launches", secondBody, "test-token")
	decodeResp(t, res, &second)
	if second["playspec_id"] != first["playspec_id"] {
		t.Fatalf("same-compose launch should reuse existing playspec, first=%#v second=%#v", first, second)
	}
	if numberID(second["playground_id"]) == "0" {
		t.Fatalf("second launch should create a playground from the reused playspec: %#v", second)
	}

	playground, err := st.GetPlayground(context.Background(), "replayed-launch")
	if err != nil {
		t.Fatalf("get replayed playground: %v", err)
	}
	if playground.PlayspecID == nil || idString(*playground.PlayspecID) != numberID(first["playspec_id"]) {
		t.Fatalf("playground should reference reused playspec, pg=%#v first=%#v", playground, first)
	}
}

func TestLaunchDuplicateDifferentComposeConflicts(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	firstBody := map[string]any{
		"name":              "conflicting-launch-playspec",
		"create_playground": false,
		"compose_yaml":      "services:\n  web:\n    image: nginx:alpine\n",
	}
	var first map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", firstBody, "test-token")
	decodeResp(t, res, &first)

	secondBody := map[string]any{
		"name":              "conflicting-launch-playspec",
		"create_playground": false,
		"compose_yaml":      "services:\n  web:\n    image: httpd:alpine\n",
	}
	res = doReq(t, srv, http.MethodPost, "/api/launches", secondBody, "test-token")
	if res.StatusCode != http.StatusConflict {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("different-compose duplicate launch should return 409, got %d: %#v", res.StatusCode, got)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	closeResponseBody(t, res)
	if got["error"].(map[string]any)["code"] != "RESOURCE_IN_USE" {
		t.Fatalf("unexpected conflict body: %#v", got)
	}
	existing, err := st.GetPlayspec(context.Background(), "conflicting-launch-playspec")
	if err != nil {
		t.Fatalf("existing playspec should remain: %v", err)
	}
	if !strings.Contains(existing.BaseComposeYAML, "nginx:alpine") {
		t.Fatalf("existing playspec should not be overwritten: %#v", existing)
	}
}

func TestLaunchComposePathCreatesPlayspecFromFile(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	composePath := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "path-compose-launch",
		"create_playground": false,
		"compose_yaml":      composePath,
	}, "test-token")
	decodeResp(t, res, &launch)
	playspec, err := st.GetPlayspec(context.Background(), numberID(launch["playspec_id"]))
	if err != nil {
		t.Fatalf("get path launch playspec: %v", err)
	}
	if !strings.Contains(playspec.BaseComposeYAML, "image: nginx:alpine") {
		t.Fatalf("expected file contents in playspec, got:\n%s", playspec.BaseComposeYAML)
	}
}

func TestLaunchComposePathRejectedForRemoteRequests(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	composePath := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx:alpine\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/launches", strings.NewReader(fmt.Sprintf(`{"name":"remote-path-compose","create_playground":false,"compose_yaml":%q}`, composePath)))
	req.RemoteAddr = "203.0.113.10:4321"
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected remote path request to fail with bad request, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "loopback") {
		t.Fatalf("expected loopback explanation, got %s", rec.Body.String())
	}
	if _, err := st.GetPlayspec(context.Background(), "remote-path-compose"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remote path request should not create playspec, got err=%v", err)
	}
}

func TestLaunchComposePathRejectsNonRegularFile(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	composePath := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.Mkdir(composePath, 0o700); err != nil {
		t.Fatalf("create compose path directory: %v", err)
	}
	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "nonregular-compose-path",
		"create_playground": false,
		"compose_yaml":      composePath,
	}, "test-token")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected non-regular path to fail, got %d", res.StatusCode)
	}
	if _, err := st.GetPlayspec(context.Background(), "nonregular-compose-path"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("non-regular path request should not create playspec, got err=%v", err)
	}
}

func TestLaunchComposePathRejectsOversizedFile(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	composePath := filepath.Join(t.TempDir(), "docker-compose.yml")
	file, err := os.Create(composePath)
	if err != nil {
		t.Fatalf("create compose file: %v", err)
	}
	if err := file.Truncate((16 << 20) + 1); err != nil {
		_ = file.Close()
		t.Fatalf("grow compose file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close compose file: %v", err)
	}
	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "oversized-compose-path",
		"create_playground": false,
		"compose_yaml":      composePath,
	}, "test-token")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected oversized path to fail, got %d", res.StatusCode)
	}
	if _, err := st.GetPlayspec(context.Background(), "oversized-compose-path"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("oversized path request should not create playspec, got err=%v", err)
	}
}

func TestLaunchDuplicateFailedPlaygroundIsNoOpReplay(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()

	ctx := context.Background()
	domains := "apps.example.test"
	https := true
	acmeEmail := "ops@example.test"
	marquee, err := st.EnsureConfiguredMarquee(ctx, domain.Marquee{
		Name:          store.ConfiguredMarqueeName,
		Host:          "127.0.0.1",
		Port:          22,
		User:          "ubuntu",
		DomainsInput:  &domains,
		HTTPSEnabled:  &https,
		AcmeEmail:     &acmeEmail,
		SSHPrivateKey: "test",
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("create configured marquee: %v", err)
	}
	composeYAML := `services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
`
	var setup map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "retry-launch",
		"create_playground": false,
		"compose_yaml":      composeYAML,
	}, "test-token")
	decodeResp(t, res, &setup)
	playspec, err := st.GetPlayspec(ctx, numberID(setup["playspec_id"]))
	if err != nil {
		t.Fatalf("get setup playspec: %v", err)
	}
	oldError := "old ssh failure"
	existing, err := st.CreatePlayground(ctx, domain.Playground{
		Name:                 "retry-launch",
		Status:               domain.StatusError,
		PlayspecID:           playspec.ID,
		MarqueeID:            &marquee.ID,
		GeneratedComposeYAML: "stale",
		ErrorMessage:         &oldError,
		CreationSteps: []domain.PlaygroundCreationStep{{
			Name:         "host_prerequisites",
			Status:       "error",
			ErrorMessage: oldError,
		}},
	})
	if err != nil {
		t.Fatalf("create failed playground: %v", err)
	}

	var launch map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":         "retry-launch",
		"compose_yaml": composeYAML,
	}, "test-token")
	decodeResp(t, res, &launch)
	if got, want := numberID(launch["playground_id"]), idString(existing.ID); got != want {
		t.Fatalf("failed playground replay should return existing row, got %s want %s", got, want)
	}
	replayed, err := st.GetPlayground(ctx, "retry-launch")
	if err != nil {
		t.Fatalf("get replayed playground: %v", err)
	}
	if replayed.Status != domain.StatusError {
		t.Fatalf("failed playground replay should not redeploy, got status=%s error=%v", replayed.Status, replayed.ErrorMessage)
	}
	if replayed.ErrorMessage == nil || *replayed.ErrorMessage != oldError || !strings.Contains(replayed.GeneratedComposeYAML, "stale") {
		t.Fatalf("replay should preserve failure diagnostics: %#v", replayed)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("replay should not reach runtime deployment, got %#v", fake.Seen)
	}
}

func TestRepositoryLaunchReturnsBeforeRemoteDeployCompletes(t *testing.T) {
	srv, st := newTestServerWithStore(t, failingRemoteDeployExecutor())
	defer srv.Close()
	marquee := createRepositoryLaunchMarquee(t, st)
	launch := createRepositoryLaunch(t, srv, marquee.ID)

	waitForAsyncLaunchError(t, st, numberID(launch["playground_id"]))
}

func TestLaunchRepositoryPropDuplicateDifferentRepoIsRejectedAndCleansUpPlayspec(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	if _, err := st.CreateProp(context.Background(), domain.Prop{Name: "conflicting-repo", RepositoryURL: "ssh://git.example.test/acme/one.git"}); err != nil {
		t.Fatalf("create existing prop: %v", err)
	}
	body := map[string]any{
		"name":              "conflicting-repo",
		"repository_url":    "ssh://git.example.test/acme/two.git",
		"create_playground": false,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
`,
	}
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	if res.StatusCode != http.StatusConflict {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected conflicting prop repository to return 409, got %d: %#v", res.StatusCode, got)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	closeResponseBody(t, res)
	if got["error"].(map[string]any)["code"] != "RESOURCE_IN_USE" {
		t.Fatalf("unexpected conflict body: %#v", got)
	}
	if _, err := st.GetPlayspec(context.Background(), "conflicting-repo"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("launch-created playspec should be cleaned up after prop conflict, got %v", err)
	}
}

func TestLaunchRepositoryConflictDoesNotDeletePreexistingPlayspec(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	composeYAML := `services:
  web:
    image: nginx:alpine
`
	existing, err := st.CreatePlayspec(context.Background(), domain.Playspec{Name: "preexisting-conflict", BaseComposeYAML: composeYAML})
	if err != nil {
		t.Fatalf("create existing playspec: %v", err)
	}
	if _, err := st.CreateProp(context.Background(), domain.Prop{Name: "preexisting-conflict", RepositoryURL: "ssh://git.example.test/acme/one.git"}); err != nil {
		t.Fatalf("create existing prop: %v", err)
	}
	body := map[string]any{
		"name":              "preexisting-conflict",
		"repository_url":    "ssh://git.example.test/acme/two.git",
		"create_playground": false,
		"compose_yaml":      composeYAML,
	}
	res := doReq(t, srv, http.MethodPost, "/api/launches", body, "test-token")
	if res.StatusCode != http.StatusConflict {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected conflicting prop repository to return 409, got %d: %#v", res.StatusCode, got)
	}
	closeResponseBody(t, res)
	persisted, err := st.GetPlayspec(context.Background(), "preexisting-conflict")
	if err != nil {
		t.Fatalf("preexisting playspec should not be cleaned up: %v", err)
	}
	if persisted.ID == nil || existing.ID == nil || *persisted.ID != *existing.ID {
		t.Fatalf("wrong playspec persisted after conflict: existing=%#v persisted=%#v", existing, persisted)
	}
}

func TestLaunchCreatePassesServiceAuthPasswordOverrideToPlayground(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","State":"running","Health":"healthy","Image":"nginx:alpine"}]`},
		},
	}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()

	domains := "example.test"
	marquee := ensureTestConfiguredMarqueeWith(t, st, domain.Marquee{DomainsInput: &domains, SSHPrivateKey: "key"})

	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":              "launch-auth-pg",
		"marquee_id":        marquee.ID,
		"create_playground": true,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      fibe.gg/visibility: internal
`,
		"services": map[string]any{
			"web": map[string]any{"auth_password": "service-password"},
		},
	}, "test-token")
	decodeResp(t, res, &launch)

	playgroundID := numberID(launch["playground_id"])
	var pg map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/playgrounds/"+playgroundID, nil, "test-token")
	decodeResp(t, res, &pg)

	stored, err := st.GetPlayground(context.Background(), playgroundID)
	if err != nil {
		t.Fatalf("get stored playground: %v", err)
	}
	rendered := stored.GeneratedComposeYAML
	if strings.Contains(rendered, "service-password") {
		t.Fatalf("rendered compose must not contain raw service auth password:\n%s", rendered)
	}
	assertRenderedServiceAuthPassword(t, rendered, playgroundID, "service-password", stringFromMap(pg, "internal_password"))
}

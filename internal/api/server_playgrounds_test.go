package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	store "github.com/fibegg/fibe-distilled/internal/storage"

	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	"github.com/fibegg/fibe-distilled/internal/worker"
)

func TestPlaygroundOptionalBodyEndpointsRejectMalformedJSON(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "optional-body-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "optional-body-pg",
		"playspec_id": int64(playspec["id"].(float64)),
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)

	for _, path := range []string{
		"/api/playgrounds/optional-body-pg/logs",
		"/api/playgrounds/optional-body-pg/operations",
		"/api/playgrounds/optional-body-pg/expiration",
	} {
		res = doRawPost(t, srv, path, "{")
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s should reject malformed JSON, got %d", path, res.StatusCode)
		}
		closeResponseBody(t, res)

		res = doRawPost(t, srv, path, "{} {}")
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s should reject multiple JSON values, got %d", path, res.StatusCode)
		}
		closeResponseBody(t, res)

		res = doRawPost(t, srv, path, "null")
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s should reject top-level JSON null, got %d", path, res.StatusCode)
		}
		closeResponseBody(t, res)
	}
}

func TestPlaygroundOptionalBodyEndpointsRejectExplicitNoOps(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "optional-noop-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "optional-noop-pg",
		"playspec_id": int64(playspec["id"].(float64)),
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)

	badRequests := []badRequestCase{
		{name: "logs null service", path: "/api/playgrounds/optional-noop-pg/logs", body: map[string]any{"service": nil}},
		{name: "logs blank service", path: "/api/playgrounds/optional-noop-pg/logs", body: map[string]any{"service": ""}},
		{name: "logs null tail", path: "/api/playgrounds/optional-noop-pg/logs", body: map[string]any{"tail": nil}},
		{name: "operation missing action_type", path: "/api/playgrounds/optional-noop-pg/operations", body: map[string]any{}},
		{name: "operation blank action_type", path: "/api/playgrounds/optional-noop-pg/operations", body: map[string]any{"action_type": ""}},
		{name: "operation null force", path: "/api/playgrounds/optional-noop-pg/operations", body: map[string]any{"force": nil}},
		{name: "expiration null duration", path: "/api/playgrounds/optional-noop-pg/expiration", body: map[string]any{"duration_hours": nil}},
		{name: "expiration zero duration", path: "/api/playgrounds/optional-noop-pg/expiration", body: map[string]any{"duration_hours": 0}},
		{name: "expiration negative duration", path: "/api/playgrounds/optional-noop-pg/expiration", body: map[string]any{"duration_hours": -1}},
		{name: "expiration overflowing duration", path: "/api/playgrounds/optional-noop-pg/expiration", body: map[string]any{"duration_hours": 999999999}},
	}
	assertBadRequestCases(t, srv, http.MethodPost, badRequests, "explicit no-op field")

	res = doReq(t, srv, http.MethodPost, "/api/playgrounds/optional-noop-pg/operations", map[string]any{"playground": nil}, "test-token")
	assertErrorCode(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestPlaygroundCreateRejectsUncompiledTemplateVariables(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	marquee := ensureTestConfiguredMarquee(t, st)
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", map[string]any{"playspec": map[string]any{
		"name": "templated-port-playspec",
		"base_compose_yaml": `x-fibe.gg:
  variables:
    PORT:
      name: Port
      default: "3000"
services:
  web:
    image: nginx
    labels:
      fibe.gg/port: $$var__PORT
`,
	}}, "test-token")
	decodeResp(t, res, &playspec)

	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", map[string]any{"playground": map[string]any{
		"name":        "uncompiled-template-pg",
		"playspec_id": numberID(playspec["id"]),
		"marquee_id":  marquee.ID,
	}}, "test-token")
	assertErrorMessageContains(t, res, http.StatusUnprocessableEntity, "INVALID_SERVICE_OVERRIDES", "unresolved Fibe template variables")
}

func TestPlaygroundCreateNoOpsCompatibleReplay(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "replay-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	body := map[string]any{"playground": map[string]any{
		"name":        "replay-pg",
		"playspec_id": int64(playspec["id"].(float64)),
	}}
	var first map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", body, "test-token")
	decodeResp(t, res, &first)

	var second map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", body, "test-token")
	decodeResp(t, res, &second)

	if second["id"] != first["id"] || second["name"] != "replay-pg" {
		t.Fatalf("compatible replay should return existing playground, first=%#v second=%#v", first, second)
	}

	conflicting := map[string]any{"playground": map[string]any{
		"name":          "replay-pg",
		"playspec_id":   int64(playspec["id"].(float64)),
		"env_overrides": map[string]string{"DIFFERENT": "1"},
	}}
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", conflicting, "test-token")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("incompatible same-name create should conflict, got %d", res.StatusCode)
	}
	var errBody map[string]any
	if err := json.NewDecoder(res.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	closeResponseBody(t, res)
	if errBody["error"].(map[string]any)["code"] != "RESOURCE_IN_USE" {
		t.Fatalf("unexpected conflict body: %#v", errBody)
	}

	expiringReplay := map[string]any{"playground": map[string]any{
		"name":         "replay-expiring-pg",
		"playspec_id":  int64(playspec["id"].(float64)),
		"never_expire": false,
	}}
	var firstExpiring map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", expiringReplay, "test-token")
	decodeResp(t, res, &firstExpiring)
	if firstExpiring["expires_at"] == nil {
		t.Fatalf("never_expire=false replay fixture should have expires_at: %#v", firstExpiring)
	}
	var secondExpiring map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", expiringReplay, "test-token")
	decodeResp(t, res, &secondExpiring)
	if secondExpiring["id"] != firstExpiring["id"] {
		t.Fatalf("same expiring request should replay existing playground, first=%#v second=%#v", firstExpiring, secondExpiring)
	}

	for _, conflictingExpiration := range []map[string]any{
		{"playground": map[string]any{
			"name":         "replay-pg",
			"playspec_id":  int64(playspec["id"].(float64)),
			"never_expire": false,
		}},
		{"playground": map[string]any{
			"name":         "replay-expiring-pg",
			"playspec_id":  int64(playspec["id"].(float64)),
			"never_expire": true,
		}},
	} {
		res = doReq(t, srv, http.MethodPost, "/api/playgrounds", conflictingExpiration, "test-token")
		if res.StatusCode != http.StatusConflict {
			var got map[string]any
			_ = json.NewDecoder(res.Body).Decode(&got)
			t.Fatalf("incompatible expiration replay should conflict, got %d: %#v", res.StatusCode, got)
		}
		closeResponseBody(t, res)
	}
}

func TestPlaygroundCreateRejectsExplicitEmptyNoOpMaps(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "create-empty-maps-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	base := map[string]any{
		"name":        "create-empty-maps-pg",
		"playspec_id": int64(playspec["id"].(float64)),
	}
	for _, tt := range []struct {
		name  string
		extra map[string]any
	}{
		{name: "null env overrides", extra: map[string]any{"env_overrides": nil}},
		{name: "env overrides", extra: map[string]any{"env_overrides": map[string]any{}}},
		{name: "blank env override key", extra: map[string]any{"env_overrides": map[string]any{" ": "value"}}},
		{name: "null env override value", extra: map[string]any{"env_overrides": map[string]any{"PORT": nil}}},
		{name: "array env override value", extra: map[string]any{"env_overrides": map[string]any{"PORT": []any{8080}}}},
		{name: "object env override value", extra: map[string]any{"env_overrides": map[string]any{"PORT": map[string]any{"value": 8080}}}},
		{name: "services", extra: map[string]any{"services": map[string]any{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{}
			for key, value := range base {
				payload[key] = value
			}
			for key, value := range tt.extra {
				payload[key] = value
			}
			res := doReq(t, srv, http.MethodPost, "/api/playgrounds", map[string]any{"playground": payload}, "test-token")
			assertBadRequest(t, res, "explicit empty map")
		})
	}
}

func TestPlaygroundCreateMissingRequiredFieldsAreValidationErrors(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "missing-required-playground-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{
			name:    "missing name",
			payload: map[string]any{"playspec_id": int64(playspec["id"].(float64))},
			want:    "playground name is required",
		},
		{
			name:    "missing playspec",
			payload: map[string]any{"name": "missing-playspec-pg"},
			want:    "playspec_id is required",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res := doReq(t, srv, http.MethodPost, "/api/playgrounds", map[string]any{"playground": tt.payload}, "test-token")
			assertErrorMessageContains(t, res, http.StatusUnprocessableEntity, "VALIDATION_ERROR", tt.want)
		})
	}
}

func TestPlaygroundCreateAcceptsScalarEnvOverrides(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "scalar-env-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	createBody := map[string]any{"playground": map[string]any{
		"name":        "scalar-env-pg",
		"playspec_id": int64(playspec["id"].(float64)),
		"env_overrides": map[string]any{
			"PORT":  8080,
			"DEBUG": true,
			"TAG":   1.25,
			"EMPTY": "",
		},
	}}
	var created map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", createBody, "test-token")
	decodeResp(t, res, &created)

	stored, err := st.GetPlayground(context.Background(), "scalar-env-pg")
	if err != nil {
		t.Fatalf("get stored playground: %v", err)
	}
	want := map[string]string{"PORT": "8080", "DEBUG": "true", "TAG": "1.25", "EMPTY": ""}
	if !equalStringMaps(stored.EnvOverrides, want) {
		t.Fatalf("env_overrides not normalized: %#v", stored.EnvOverrides)
	}
	assertRenderedEnv(t, stored.GeneratedComposeYAML, "web", want)
}

func TestPlaygroundUpdateAllowsEmptyEnvOverridesAsClear(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "clear-env-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	createBody := map[string]any{"playground": map[string]any{
		"name":          "clear-env-pg",
		"playspec_id":   int64(playspec["id"].(float64)),
		"env_overrides": map[string]any{"REMOVE_ME": "1"},
	}}
	var created map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", createBody, "test-token")
	decodeResp(t, res, &created)

	res = doReq(t, srv, http.MethodPatch, "/api/playgrounds/clear-env-pg", map[string]any{"playground": map[string]any{"env_overrides": map[string]any{}}}, "test-token")
	assertStatus(t, res, http.StatusOK, "empty env_overrides update should clear")
	closeResponseBody(t, res)

	stored, err := st.GetPlayground(context.Background(), "clear-env-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if len(stored.EnvOverrides) != 0 {
		t.Fatalf("env_overrides should be cleared, got %#v", stored.EnvOverrides)
	}

	res = doReq(t, srv, http.MethodPatch, "/api/playgrounds/clear-env-pg", map[string]any{"playground": map[string]any{"services": map[string]any{}}}, "test-token")
	assertStatus(t, res, http.StatusBadRequest, "empty services update should be rejected")
	closeResponseBody(t, res)
}

func TestPlaygroundUpdateSavesFromCurrentRow(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st})
	srv := httptest.NewServer(app)
	defer srv.Close()
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:         "current-row-update-pg",
		Status:       domain.StatusRunning,
		EnvOverrides: map[string]string{"OLD": "1"},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	current := pg
	current.Status = domain.StatusStopped
	current.Services = []domain.PlaygroundServiceInfo{{Name: "web", Image: "nginx:alpine", Status: "exited"}}
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save current row: %v", err)
	}
	res := doReq(t, srv, http.MethodPatch, "/api/playgrounds/current-row-update-pg", map[string]any{"playground": map[string]any{"env_overrides": map[string]any{"NEW": "2"}}}, "test-token")
	assertStatus(t, res, http.StatusOK, "save playground update")
	closeResponseBody(t, res)
	updated, err := st.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		t.Fatalf("reload playground: %v", err)
	}
	if updated.Status != domain.StatusStopped {
		t.Fatalf("update should preserve current status, got %#v", updated)
	}
	if _, ok := updated.EnvOverrides["OLD"]; ok || updated.EnvOverrides["NEW"] != "2" {
		t.Fatalf("update should apply requested env override replacement, got %#v", updated.EnvOverrides)
	}
	if len(updated.Services) != 1 || updated.Services[0].Image != "nginx:alpine" {
		t.Fatalf("update should preserve current runtime services, got %#v", updated.Services)
	}
}

func TestPlaygroundRuntimeConfigUpdateMarksHasChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st})
	srv := httptest.NewServer(app)
	defer srv.Close()
	for _, status := range []string{domain.StatusPending, domain.StatusInProgress, domain.StatusRunning} {
		assertRuntimeConfigUpdateMarksHasChanges(t, ctx, st, srv, status)
	}
}

func TestPlaygroundRuntimeConfigNoopUpdateKeepsCurrentStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st})
	srv := httptest.NewServer(app)
	defer srv.Close()
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:         "runtime-noop-update-pg",
		Status:       domain.StatusRunning,
		EnvOverrides: map[string]string{"SAME": "1"},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	res := doReq(t, srv, http.MethodPatch, "/api/playgrounds/runtime-noop-update-pg", map[string]any{"playground": map[string]any{"env_overrides": map[string]any{"SAME": "1"}}}, "test-token")
	assertStatus(t, res, http.StatusOK, "runtime no-op update")
	closeResponseBody(t, res)
	updated, err := st.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		t.Fatalf("reload playground: %v", err)
	}
	if updated.Status != domain.StatusRunning {
		t.Fatalf("same-value runtime config update should keep running status, got %#v", updated)
	}
	if updated.StateReason != nil {
		t.Fatalf("same-value runtime config update should not set state reason, got %#v", updated.StateReason)
	}
}

func TestPlaygroundMetadataUpdateKeepsCurrentStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st})
	srv := httptest.NewServer(app)
	defer srv.Close()
	if _, err := st.CreatePlayground(ctx, domain.Playground{
		Name:   "metadata-update-pg",
		Status: domain.StatusRunning,
	}); err != nil {
		t.Fatalf("create playground: %v", err)
	}
	res := doReq(t, srv, http.MethodPatch, "/api/playgrounds/metadata-update-pg", map[string]any{"playground": map[string]any{"name": "metadata-update-renamed"}}, "test-token")
	assertStatus(t, res, http.StatusOK, "metadata update")
	closeResponseBody(t, res)
	updated, err := st.GetPlayground(ctx, "metadata-update-renamed")
	if err != nil {
		t.Fatalf("reload renamed playground: %v", err)
	}
	if updated.Status != domain.StatusRunning {
		t.Fatalf("metadata update should keep running status, got %#v", updated)
	}
	if updated.Name != "metadata-update-renamed" {
		t.Fatalf("metadata update should rename playground, got %#v", updated)
	}
}

func TestPlaygroundCreateAcceptsNamedReferences(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "named-ref-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	marquee := ensureTestConfiguredMarquee(t, st)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "named-ref-pg",
		"playspec_id": "named-ref-spec",
		"marquee_id":  store.ConfiguredMarqueeName,
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)
	if pg["playspec_id"] != playspec["id"] || numberID(pg["marquee_id"]) != idString(marquee.ID) {
		t.Fatalf("expected named references to resolve, pg=%#v playspec=%#v marquee=%#v", pg, playspec, marquee)
	}
}

func TestStalePlaygroundSaveDoesNotOverwriteRename(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "stale-rename-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "stale-rename-pg",
		"playspec_id": int64(playspec["id"].(float64)),
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)
	pgID := int64(pg["id"].(float64))

	stale, err := st.GetPlayground(context.Background(), idString(pgID))
	if err != nil {
		t.Fatalf("load stale playground copy: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	res = doReq(t, srv, http.MethodPatch, "/api/playgrounds/stale-rename-pg", map[string]any{
		"playground": map[string]any{"name": "stale-rename-pg-renamed"},
	}, "test-token")
	decodeResp(t, res, &pg)
	if pg["name"] != "stale-rename-pg-renamed" {
		t.Fatalf("rename did not persist: %#v", pg)
	}

	stale.Status = domain.StatusInProgress
	if _, err := st.SavePlayground(context.Background(), stale); err != nil {
		t.Fatalf("save stale playground copy: %v", err)
	}

	res = doReq(t, srv, http.MethodGet, "/api/playgrounds/stale-rename-pg-renamed", nil, "test-token")
	decodeResp(t, res, &pg)
	if pg["id"] != float64(pgID) || pg["name"] != "stale-rename-pg-renamed" {
		t.Fatalf("stale save should preserve renamed lookup, got %#v", pg)
	}

	res = doReq(t, srv, http.MethodDelete, "/api/playgrounds/stale-rename-pg-renamed", nil, "test-token")
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("delete by renamed lookup should still work, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)
}

func TestPlaygroundExpirationMatchesFibeSemantics(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "expiring",
		"base_compose_yaml": `services:
  web:
    image: alpine
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)
	playspecID := int64(playspec["id"].(float64))

	future := time.Now().UTC().Add(10 * time.Hour).Truncate(time.Second)
	pgBody := map[string]any{"playground": map[string]any{
		"name":        "expiring-pg",
		"playspec_id": playspecID,
		"expires_at":  future,
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)
	assertExpirationSummary(t, pg)

	var ext expirationResponse
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds/expiring-pg/expiration", map[string]any{"duration_hours": 2}, "test-token")
	decodeResp(t, res, &ext)
	assertExpirationExtension(t, ext, future)

	defaultTTLBody := map[string]any{"playground": map[string]any{
		"name":         "default-ttl-pg",
		"playspec_id":  playspecID,
		"never_expire": false,
	}}
	start := time.Now().UTC()
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", defaultTTLBody, "test-token")
	decodeResp(t, res, &pg)
	assertDefaultTTL(t, pg, start)
	assertInvalidCreateExpirations(t, srv, playspecID)
	res = doReq(t, srv, http.MethodPatch, "/api/playgrounds/expiring-pg", map[string]any{"playground": map[string]any{
		"expires_at": "not-a-timestamp",
	}}, "test-token")
	assertBadRequest(t, res, "invalid update expires_at")

	loaded, err := st.GetPlayground(context.Background(), "default-ttl-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	for _, blockedStatus := range []string{domain.StatusStopped, domain.StatusDestroying} {
		loaded.Status = blockedStatus
		if _, err := st.SavePlayground(context.Background(), loaded); err != nil {
			t.Fatalf("save %s playground: %v", blockedStatus, err)
		}
		res = doReq(t, srv, http.MethodPost, "/api/playgrounds/default-ttl-pg/expiration", nil, "test-token")
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s playground extension should be rejected, got %d", blockedStatus, res.StatusCode)
		}
		closeResponseBody(t, res)
	}
}

func TestPlaygroundExpirationExtensionUsesCurrentRowState(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st})
	expires := time.Now().UTC().Add(time.Hour)
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:      "current-row-expiration-pg",
		Status:    domain.StatusRunning,
		ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	current := pg
	current.Status = domain.StatusStopped
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save stopped current row: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playgrounds/current-row-expiration-pg/expiration", nil)

	extension := app.extendPlaygroundExpiration(rec, req, pg, time.Hour)
	if extension.ok {
		t.Fatalf("stale running row should not allow extending current stopped row")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected invalid state response, got %d: %s", rec.Code, rec.Body.String())
	}
	persisted, err := st.GetPlayground(ctx, "current-row-expiration-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("extension must preserve stopped status, got %#v", persisted)
	}
	if persisted.ExpiresAt == nil || !persisted.ExpiresAt.Equal(expires) {
		t.Fatalf("extension must not change current expiration, got %#v want %s", persisted.ExpiresAt, expires)
	}
}

func TestPlaygroundCreateRejectsBuildOverridesYAML(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name":              "build-overrides-spec",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	pgBody := map[string]any{"playground": map[string]any{
		"name":                 "build-overrides-pg",
		"playspec_id":          int64(playspec["id"].(float64)),
		"build_overrides_yaml": map[string]any{"web": map[string]any{"target": "runtime"}},
	}}
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("build_overrides_yaml should be rejected as unsupported, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode build override error: %v", err)
	}
	closeResponseBody(t, res)
	if body["error"].(map[string]any)["code"] != "NOT_IMPLEMENTED" {
		t.Fatalf("unexpected build override error: %#v", body)
	}
}

func TestPlaygroundUpdateHandlerRejectsBuildOverridesYAMLWithoutGate(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ps, err := st.CreatePlayspec(context.Background(), domain.Playspec{
		Name:            "update-build-overrides-spec",
		BaseComposeYAML: "services:\n  web:\n    image: alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(context.Background(), domain.Playground{
		Name:       "update-build-overrides-pg",
		PlayspecID: ps.ID,
		Status:     domain.StatusRunning,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{
		DB: st,
		Runtime: runtime.Checker{
			Executor: &runtimetest.FakeExecutor{},
		},
	})

	body := `{"playground":{"build_overrides_yaml":{"web":{"target":"runtime"}}}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/playgrounds/"+pg.Name, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("handler should reject build_overrides_yaml without relying on compatgate, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestResourcePatchRejectsEmptyPayloads(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ctx := context.Background()

	prop, err := st.CreateProp(ctx, domain.Prop{
		Name:          "empty-update-prop",
		RepositoryURL: "https://github.com/acme/empty-update-prop.git",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "empty-update-spec",
		BaseComposeYAML: "services:\n  web:\n    image: alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:       "empty-update-pg",
		PlayspecID: ps.ID,
		Status:     domain.StatusRunning,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	cases := []badRequestCase{
		{name: "prop", path: "/api/props/" + prop.Name, body: map[string]any{"prop": map[string]any{}}},
		{name: "playspec", path: "/api/playspecs/" + ps.Name, body: map[string]any{"playspec": map[string]any{}}},
		{name: "playground", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{}}},
		{name: "playground empty services", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"services": map[string]any{}}}},
		{name: "playground null expires_at", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"expires_at": nil}}},
	}
	assertBadRequestCases(t, srv, http.MethodPatch, cases, "empty update")
}

func TestResourcePatchRejectsExplicitBlankAndNullFields(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ctx := context.Background()

	prop, err := st.CreateProp(ctx, domain.Prop{
		Name:          "blank-update-prop",
		RepositoryURL: "https://github.com/acme/blank-update-prop.git",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	description := "temporary description"
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "blank-update-spec",
		Description:     &description,
		BaseComposeYAML: "services:\n  web:\n    image: alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:       "blank-update-pg",
		PlayspecID: ps.ID,
		Status:     domain.StatusRunning,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	cases := []badRequestCase{
		{name: "prop blank repository", path: "/api/props/" + prop.Name, body: map[string]any{"prop": map[string]any{"repository_url": ""}}},
		{name: "prop null private", path: "/api/props/" + prop.Name, body: map[string]any{"prop": map[string]any{"private": nil}}},
		{name: "playspec null services", path: "/api/playspecs/" + ps.Name, body: map[string]any{"playspec": map[string]any{"services": nil}}},
		{name: "playground blank marquee", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"marquee_id": ""}}},
		{name: "playground zero playspec id", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"playspec_id": 0}}},
		{name: "playground negative marquee id", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"marquee_id": -1}}},
		{name: "playground fractional playspec id", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"playspec_id": 1.5}}},
		{name: "playground boolean marquee id", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"marquee_id": true}}},
		{name: "playground object marquee id", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"marquee_id": map[string]any{"id": 1}}}},
		{name: "playground blank env override key", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"env_overrides": map[string]any{" ": "value"}}}},
		{name: "playground null env override value", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"env_overrides": map[string]any{"PORT": nil}}}},
		{name: "playground object env override value", path: "/api/playgrounds/" + pg.Name, body: map[string]any{"playground": map[string]any{"env_overrides": map[string]any{"PORT": map[string]any{"value": 8080}}}}},
	}
	assertBadRequestCases(t, srv, http.MethodPatch, cases, "blank/null field")

	var updated map[string]any
	res := doReq(t, srv, http.MethodPatch, "/api/playspecs/"+ps.Name, map[string]any{"playspec": map[string]any{"description": nil}}, "test-token")
	decodeResp(t, res, &updated)
	if updated["description"] != nil {
		t.Fatalf("description:null should clear description, got %#v", updated)
	}
}

func TestPlaygroundHandlersRejectReservedServiceOverridesWithoutGate(t *testing.T) {
	app, playspecID, playgroundName := newReservedOverrideHandlerApp(t)
	for _, tc := range reservedServiceOverrideCases(playspecID, playgroundName) {
		t.Run(tc.name, func(t *testing.T) {
			assertServiceOverrideRejectedByHandler(t, app, tc.method, tc.path, tc.body)
		})
	}
}

type serviceOverrideCase struct {
	name   string
	method string
	path   string
	body   string
}

func TestPlaygroundCreateAppliesServiceOverridesToRuntimeCompose(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "override-spec",
		"base_compose_yaml": `services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
    volumes:
      - ${FIBE_SERVICES_WEB_PATH}:/app
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "override-pg",
		"playspec_id": int64(playspec["id"].(float64)),
		"env_overrides": map[string]string{
			"GLOBAL_VAR": "global",
		},
		"services": map[string]any{
			"web": map[string]any{
				"env_vars":            map[string]any{"SERVICE_VAR": "service", "SERVICE_PORT": 8080, "SERVICE_DEBUG": true},
				"subdomain":           "demo",
				"exposure_port":       3000,
				"exposure_visibility": "internal",
				"path_rule":           "PathPrefix(`/demo`)",
				"git_config":          map[string]any{"branch_name": "feature/demo"},
				"repo_url":            "https://github.com/acme/demo-override.git",
				"dockerfile_path":     "deploy/Dockerfile",
				"build_target":        "runner",
				"build_args":          "NODE_VERSION=22,APP_ENV=test",
			},
		},
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)
	if pg["service_branches"] == nil {
		t.Fatalf("expected persisted service overrides: %#v", pg)
	}

	stored, err := st.GetPlayground(context.Background(), "override-pg")
	if err != nil {
		t.Fatalf("get stored playground: %v", err)
	}
	rendered := stored.GeneratedComposeYAML
	for _, want := range []string{
		"GLOBAL_VAR: global",
		"SERVICE_VAR: service",
		"SERVICE_PORT: \"8080\"",
		"SERVICE_DEBUG: \"true\"",
		"fibe.gg/branch: feature/demo",
		"fibe.gg/repo_url: https://github.com/acme/demo-override.git",
		"fibe.gg/dockerfile: deploy/Dockerfile",
		"fibe.gg/build_target: runner",
		"fibe.gg/build_args: NODE_VERSION=22,APP_ENV=test",
		"fibe.gg/port: \"3000\"",
		"fibe.gg/visibility: internal",
		"/opt/fibe/playgrounds/",
		"/props/acme-demo-override/feature-demo:/app",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in compose:\n%s", want, rendered)
		}
	}
}

func TestPlaygroundServiceOverridesValidateRenderedCompose(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "override-validation-spec",
		"base_compose_yaml": `services:
  web:
    image: nginx:alpine
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)
	playspecID := int64(playspec["id"].(float64))

	invalid := map[string]any{"web": map[string]any{"subdomain": "Bad_Subdomain"}}
	cases := []struct {
		name     string
		services any
	}{
		{name: "invalid-override", services: invalid},
		{name: "unknown-service-override", services: map[string]any{"api": map[string]any{"subdomain": "api"}}},
		{name: "malformed-service-override", services: map[string]any{"web": "not-an-object"}},
		{name: "malformed-env-vars-override", services: map[string]any{"web": map[string]any{"env_vars": "DEBUG=1"}}},
		{name: "nonscalar-service-env-vars", services: map[string]any{"web": map[string]any{"env_vars": map[string]any{"PORT": map[string]any{"value": 8080}}}}},
		{name: "empty-service-override", services: map[string]any{"web": map[string]any{}}},
		{name: "empty-service-env-vars", services: map[string]any{"web": map[string]any{"env_vars": map[string]any{}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := doReq(t, srv, http.MethodPost, "/api/playgrounds", playgroundServiceOverrideBody(tc.name+"-pg", playspecID, tc.services), "test-token")
			assertErrorCode(t, res, http.StatusUnprocessableEntity, "INVALID_SERVICE_OVERRIDES")
		})
	}

	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", playgroundServiceOverrideBody("valid-override-pg", playspecID, map[string]any{"web": map[string]any{
		"subdomain":     "valid-web",
		"exposure_port": 80,
	}}), "test-token")
	var pg map[string]any
	decodeResp(t, res, &pg)

	res = doReq(t, srv, http.MethodPatch, "/api/playgrounds/valid-override-pg", map[string]any{"playground": map[string]any{
		"services": invalid,
	}}, "test-token")
	assertErrorCode(t, res, http.StatusUnprocessableEntity, "INVALID_SERVICE_OVERRIDES")
}

func TestPlaygroundCreateAllowsServiceAuthPasswordOverride(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","State":"running","Health":"healthy","Image":"nginx:alpine"}]`},
		},
	}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()

	domains := "example.test"
	marquee := ensureTestConfiguredMarqueeWith(t, st, domain.Marquee{DomainsInput: &domains, SSHPrivateKey: "key"})

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "service-auth-spec",
		"base_compose_yaml": `services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      fibe.gg/visibility: internal
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "service-auth-pg",
		"playspec_id": int64(playspec["id"].(float64)),
		"marquee_id":  marquee.ID,
		"services": map[string]any{
			"web": map[string]any{"auth_password": "service-password"},
		},
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)

	stored, err := st.GetPlayground(context.Background(), "service-auth-pg")
	if err != nil {
		t.Fatalf("get stored playground: %v", err)
	}
	rendered := stored.GeneratedComposeYAML
	if strings.Contains(rendered, "service-password") {
		t.Fatalf("rendered compose must not contain raw service auth password:\n%s", rendered)
	}
	assertRenderedServiceAuthPassword(t, rendered, numberID(pg["id"]), "service-password", stringFromMap(pg, "internal_password"))
}

type currentRowLogsFixture struct {
	ctx        context.Context
	fake       *runtimetest.FakeExecutor
	app        *Server
	playground domain.Playground
}

func TestUnsupportedSurfaceIsExplicit(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	var body map[string]any
	res := doReq(t, srv, http.MethodGet, "/api/api_keys", nil, "test-token")
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
	defer closeResponseBody(t, res)
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "NOT_IMPLEMENTED" {
		t.Fatalf("unexpected error: %#v", body)
	}
}

type rolloutClaimFixture struct {
	ctx           context.Context
	store         *store.DB
	app           *Server
	playground    domain.Playground
	newPlayspecID int64
}

type hardRestartReloadFixture struct {
	ctx         context.Context
	store       *store.DB
	app         *Server
	playground  domain.Playground
	newPlayspec domain.Playspec
}

type mutateDuringOperationExecutor struct {
	runtimetest.FakeExecutor
	store        *store.DB
	playgroundID int64
	mutateOn     string
	mutate       func(context.Context, *store.DB, int64) error
	mutated      bool
	result       runtime.CommandResult
	err          error
}

func (e *mutateDuringOperationExecutor) Run(ctx context.Context, _ domain.Marquee, command string) (runtime.CommandResult, error) {
	e.Seen = append(e.Seen, command)
	matchesMutationCommand := strings.Contains(command, e.mutateOn)
	if err := e.mutateOnce(ctx, matchesMutationCommand); err != nil {
		return runtime.CommandResult{}, err
	}
	if e.err != nil && matchesMutationCommand {
		return e.result, e.err
	}
	if strings.Contains(command, "ps --all --format json") {
		return runtime.CommandResult{Stdout: `[{"Service":"web","Image":"alpine","State":"running","Health":"healthy","ExitCode":0}]`}, nil
	}
	if e.hasExplicitResult() {
		return e.result, nil
	}
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *mutateDuringOperationExecutor) mutateOnce(ctx context.Context, matchesMutationCommand bool) error {
	if e.mutated || !matchesMutationCommand {
		return nil
	}
	e.mutated = true
	if e.mutate == nil {
		return nil
	}
	return e.mutate(ctx, e.store, e.playgroundID)
}

func (e *mutateDuringOperationExecutor) hasExplicitResult() bool {
	return e.result.Stdout != "" || e.result.Stderr != "" || e.result.ExitCode != 0
}

func (e *mutateDuringOperationExecutor) WriteFile(context.Context, domain.Marquee, string, string) (runtime.CommandResult, error) {
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *mutateDuringOperationExecutor) Up(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	_, err := e.Run(ctx, marquee, runtimetest.ComposeUpCommand("FIBE_DISTILLED_UP", project, base, marquee.ID))
	return err
}

func (e *mutateDuringOperationExecutor) Start(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	_, err := e.Run(ctx, marquee, runtimetest.ComposeUpCommand("FIBE_DISTILLED_START", project, base, marquee.ID))
	return err
}

func (e *mutateDuringOperationExecutor) Stop(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	result, err := e.Run(ctx, marquee, runtimetest.ComposeStopCommand("FIBE_DISTILLED_STOP", project, base, marquee.ID))
	return commandErrorWithOutput(result, err)
}

func (e *mutateDuringOperationExecutor) Down(ctx context.Context, marquee domain.Marquee, project string, base string, _ string, removeVolumes bool) error {
	args := "down --remove-orphans"
	if removeVolumes {
		args += " -v"
	}
	result, err := e.Run(ctx, marquee, "FIBE_DISTILLED_DOWN project="+runtime.ShellQuote(project)+" base="+runtime.ShellQuote(base)+" marquee_id="+idString(marquee.ID)+" cd "+runtime.ShellQuote(base)+" && docker compose -f compose.yml -p "+runtime.ShellQuote(project)+" "+args)
	return commandErrorWithOutput(result, err)
}

func (e *mutateDuringOperationExecutor) Logs(context.Context, domain.Marquee, string, string, string, string, string) ([]string, error) {
	return []string{"ok"}, nil
}

func (e *mutateDuringOperationExecutor) Services(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) ([]domain.PlaygroundServiceInfo, error) {
	_, err := e.Run(ctx, marquee, "FIBE_DISTILLED_INSPECT "+project+" "+base+" ps --all --format json base="+runtime.ShellQuote(base)+" project="+runtime.ShellQuote(project)+" marquee_id="+idString(marquee.ID))
	if err != nil {
		return nil, err
	}
	return []domain.PlaygroundServiceInfo{{Name: "web", Image: "alpine", Status: "running", Health: "healthy", Running: true}}, nil
}

type runtimePlaygroundFixture struct {
	marqueeName       string
	playgroundName    string
	project           string
	initialStatus     string
	supersedingStatus string
}

type badRequestCase struct {
	name string
	path string
	body map[string]any
}

type expirationResponse struct {
	ID            int64     `json:"id"`
	ExpiresAt     time.Time `json:"expires_at"`
	TimeRemaining float64   `json:"time_remaining"`
}

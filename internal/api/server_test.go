package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

func idString(id int64) string {
	return strconv.FormatInt(id, 10)
}

func TestAuthAndMe(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodGet, "/up.json", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("up status=%d", res.StatusCode)
	}
	closeResponseBody(t, res)

	res = doReq(t, srv, http.MethodGet, "/api/me", nil, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized /api/me status=%d", res.StatusCode)
	}
	closeResponseBody(t, res)

	var me map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/me", nil, "test-token")
	decodeResp(t, res, &me)
	if me["email"] != "owner@fibe-distilled.local" {
		t.Fatalf("unexpected /api/me: %#v", me)
	}
	scopes := me["api_key_scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "*" {
		t.Fatalf("/api/me should expose the SDK-compatible admin scope hint, got %#v", me)
	}

	var info map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/server-info", nil, "test-token")
	decodeResp(t, res, &info)
	if info["name"] != "fibe-distilled" || strings.TrimSpace(info["server_id"].(string)) == "" {
		t.Fatalf("unexpected /api/server-info: %#v", info)
	}
}

func TestStatusDoesNotMaskStoreErrors(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	res := doReq(t, srv, http.MethodGet, "/api/status", nil, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status should fail when store reads fail, got %d", res.StatusCode)
	}
}

func TestStatusCountsCurrentActivePlaygroundStates(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	for _, status := range []string{
		domain.StatusPending,
		domain.StatusInProgress,
		domain.StatusRunning,
		domain.StatusHasChanges,
		domain.StatusCompleted,
		domain.StatusStopped,
		domain.StatusError,
	} {
		if _, err := st.CreatePlayground(context.Background(), domain.Playground{Name: "status-" + status, Status: status}); err != nil {
			t.Fatalf("create %s playground: %v", status, err)
		}
	}

	var body map[string]any
	res := doReq(t, srv, http.MethodGet, "/api/status", nil, "test-token")
	decodeResp(t, res, &body)
	playgrounds := body["playgrounds"].(map[string]any)
	if got := numberID(playgrounds["active"]); got != "4" {
		t.Fatalf("active should count pending/in_progress/running/has_changes only, got %s in %#v", got, body)
	}
	if got := numberID(playgrounds["stopped"]); got != "1" {
		t.Fatalf("stopped count should include only stopped, got %s in %#v", got, body)
	}
	if got := numberID(playgrounds["total"]); got != "7" {
		t.Fatalf("total should include all playgrounds, got %s in %#v", got, body)
	}
	if got := numberID(body["api_keys"]); got != "0" {
		t.Fatalf("status should report no stored API-key resources, got %s in %#v", got, body)
	}
}

func TestUnknownAPIPathReturnsNotImplemented(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/unknown_surface/nested", map[string]any{}, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	errBody := body["error"].(map[string]any)
	if errBody["code"] != "NOT_IMPLEMENTED" {
		t.Fatalf("expected NOT_IMPLEMENTED, got %#v", body)
	}
}

func TestRequiredBodyEndpointsRejectMultipleJSONValues(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := `{"playspec":{"name":"bad-trailing","base_compose_yaml":"services:\n  web:\n    image: alpine\n"}} {}`
	res := doRawPost(t, srv, "/api/playspecs", body)
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("required JSON body with trailing value should fail, got %d", res.StatusCode)
	}
}

func TestResourceBodiesRejectWrappedJSONNull(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	for _, tt := range []struct {
		name string
		path string
		body string
	}{
		{name: "prop", path: "/api/props", body: `{"prop":null}`},
		{name: "playspec", path: "/api/playspecs", body: `{"playspec":null}`},
		{name: "playground", path: "/api/playgrounds", body: `{"playground":null}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := doRawPost(t, srv, tt.path, tt.body)
			assertErrorMessageContains(t, res, http.StatusBadRequest, "BAD_REQUEST", "payload must be a JSON object")
		})
	}
}

func TestCompatGateDoesNotClassifyTrailingJSONBody(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := `{"playground":{"build_overrides_yaml":{"web":{}}}} {}`
	res := doRawPost(t, srv, "/api/playgrounds", body)
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("trailing JSON should reach handler parse validation, got %d", res.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got["error"].(map[string]any)["code"] != "BAD_REQUEST" {
		t.Fatalf("expected BAD_REQUEST instead of compat gate classification, got %#v", got)
	}
}

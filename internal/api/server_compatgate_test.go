package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompatGateRunsAfterAuth(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodGet, "/api/api_keys", nil, "")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated unsupported API should fail auth first, got %d", res.StatusCode)
	}
}

func TestCompatGateReturnsStructuredUnsupportedDetails(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/demo/operations", map[string]any{"action_type": "enable_maintenance"}, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("missing request id header")
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "NOT_IMPLEMENTED" {
		t.Fatalf("unexpected error: %#v", body)
	}
	details := errObj["details"].(map[string]any)
	if details["resource"] != "playgrounds" || details["operation"] != "operation" {
		t.Fatalf("unexpected details: %#v", details)
	}
}

func TestCompatGateRejectsPlaygroundOperationActionAlias(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/demo/operations", map[string]any{"action": "rollout"}, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	details := body["error"].(map[string]any)["details"].(map[string]any)
	unsupported := details["unsupported"].([]any)
	if len(unsupported) != 1 || unsupported[0] != "field:action" {
		t.Fatalf("unexpected unsupported details: %#v", details)
	}
}

func TestCompatGateOldUnsupportedPathStillExplicit(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodGet, "/api/api_keys", nil, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	details := errObj["details"].(map[string]any)
	if errObj["code"] != "NOT_IMPLEMENTED" || details["resource"] != "api_keys" {
		t.Fatalf("unexpected unsupported response: %#v", body)
	}
}

func TestCompatGateClassifiesUnsupportedSDKPropSubresources(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/props/attachments", map[string]any{"repo_full_name": "acme/demo"}, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	details := errObj["details"].(map[string]any)
	if errObj["code"] != "NOT_IMPLEMENTED" || details["resource"] != "props" || details["operation"] != "attach" {
		t.Fatalf("unexpected unsupported response: %#v", body)
	}
}

func TestCompatGateLeavesPublicEndpointsOutsideAPI(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodGet, "/up.json", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/up.json should stay public, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)
}

func TestNonAPIRouterMissesReturnStructuredJSON(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodGet, "/missing", nil, "")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected non-API miss to return 404, got %d", res.StatusCode)
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("missing request id header")
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "RESOURCE_NOT_FOUND" {
		t.Fatalf("expected structured not found, got %#v", body)
	}

	res = doReq(t, srv, http.MethodPost, "/up.json", nil, "")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected method-not-allowed JSON, got %d", res.StatusCode)
	}
}

func TestCompatGateRejectsPreviouslyIgnoredPayloadField(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := map[string]any{"playspec": map[string]any{
		"name":              "triggered",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
		"trigger_config":    map[string]any{"event_type": "pull_request"},
	}}
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", body, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("trigger_config should be rejected as unsupported, got %d", res.StatusCode)
	}

	res = doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":           "provider-selector",
		"compose_yaml":   "services:\n  web:\n    image: alpine\n",
		"repository_url": "https://github.com/acme/provider-selector.git",
		"git_provider":   nil,
	}, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("git_provider should be rejected as unsupported, got %d", res.StatusCode)
	}
}

func TestCompatGateRejectsPlayspecServiceClassificationFields(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := map[string]any{"playspec": map[string]any{
		"name":              "dynamic-classification",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
		"services": []map[string]any{{
			"name":     "web",
			"type":     "dynamic",
			"prop_id":  1,
			"workdir":  "/app",
			"workflow": "build",
		}},
	}}
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", body, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("dynamic service classification fields should be rejected as unsupported, got %d", res.StatusCode)
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("missing request id header")
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := payload["error"].(map[string]any)
	details := errObj["details"].(map[string]any)
	unsupported := details["unsupported"].([]any)
	if !anyString(unsupported, "field:services.0.prop_id") || !anyString(unsupported, "field:services.0.workdir") || !anyString(unsupported, "field:services.0.workflow") {
		t.Fatalf("unexpected unsupported details: %#v", details)
	}
}

func TestCompatGateRejectsExtraPayloadField(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := map[string]any{"playspec": map[string]any{
		"name":              "extra-field",
		"base_compose_yaml": "services:\n  web:\n    image: alpine\n",
		"client_note":       "not part of fibe-distilled API contract",
	}}
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", body, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("extra field should be rejected as unsupported, got %d", res.StatusCode)
	}
}

func TestCompatGateRejectsBodyOnReadOnlyEndpoint(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodGet, "/api/me", map[string]any{"ignored": true}, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("GET body should be rejected as unsupported, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "NOT_IMPLEMENTED" {
		t.Fatalf("unexpected error: %#v", body)
	}
	details := errObj["details"].(map[string]any)
	if !anyString(details["unsupported"].([]any), "body") {
		t.Fatalf("unexpected unsupported details: %#v", details)
	}
}

func TestCompatGateRejectsBodyOnBodylessPostEndpoint(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/props/missing/syncs", map[string]any{}, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("sync body should be rejected before resource loading, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "NOT_IMPLEMENTED" {
		t.Fatalf("unexpected error: %#v", body)
	}
	details := errObj["details"].(map[string]any)
	if details["resource"] != "props" || details["operation"] != "sync" || !anyString(details["unsupported"].([]any), "body") {
		t.Fatalf("unexpected unsupported details: %#v", details)
	}

	res = doReq(t, srv, http.MethodPost, "/api/playgrounds/missing/status", map[string]any{}, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status refresh body should be rejected before resource loading, got %d", res.StatusCode)
	}
}

func TestCompatGateRejectsPropSyncWebAlias(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/props/missing/sync", nil, "test-token")
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("singular sync alias should be unsupported, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "NOT_IMPLEMENTED" {
		t.Fatalf("unexpected error: %#v", body)
	}
	details := errObj["details"].(map[string]any)
	if details["operation"] != "sync" || !anyString(details["supported"].([]any), "POST /api/props/:id/syncs") {
		t.Fatalf("unexpected alias details: %#v", details)
	}
}

func TestCompatGateOversizedBodyUsesDecisionStatus(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/props", strings.NewReader(`{"prop":{"name":"p","repository_url":"https://github.com/acme/p.git"}}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = 17 << 20
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected oversized gate decision status, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("expected PAYLOAD_TOO_LARGE, got %#v", body)
	}
}

func anyString(values []any, want string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == want {
			return true
		}
	}
	return false
}

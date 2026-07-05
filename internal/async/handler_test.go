package async

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/storage"
)

type fakeAsyncRepo struct {
	op  domain.AsyncOperation
	err error
	got string
}

func (f *fakeAsyncRepo) GetAsync(_ context.Context, id string) (domain.AsyncOperation, error) {
	f.got = id
	if f.err != nil {
		return domain.AsyncOperation{}, f.err
	}
	return f.op, nil
}

func TestShowWritesSuccessPayloadWithDefaultStatus(t *testing.T) {
	repo := &fakeAsyncRepo{op: domain.AsyncOperation{
		ID:      "req_123",
		Status:  domain.AsyncSuccess,
		Payload: map[string]any{"playground_id": float64(42)},
	}}
	handler := NewHandler(repo)
	r := httptest.NewRequest(http.MethodGet, "/api/async/req_123", nil)
	r.SetPathValue("id", "req_123")
	w := httptest.NewRecorder()

	handler.Show(w, r)

	if repo.got != "req_123" {
		t.Fatalf("lookup id = %q", repo.got)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["request_id"] != "req_123" || body["status"] != "success" || body["playground_id"] != float64(42) {
		t.Fatalf("unexpected response body: %#v", body)
	}
}

func TestShowMapsAsyncErrorsAndMissingRows(t *testing.T) {
	errorRepo := &fakeAsyncRepo{op: domain.AsyncOperation{
		ID:     "req_failed",
		Status: domain.AsyncError,
		Error:  &domain.APIError{Code: "BROKEN", Message: "broken", Details: map[string]any{"step": "deploy"}},
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/async/req_failed", nil)
	r.SetPathValue("id", "req_failed")
	w := httptest.NewRecorder()
	NewHandler(errorRepo).Show(w, r)
	if w.Code != http.StatusOK || !containsJSONField(t, w.Body.Bytes(), "error_code", "BROKEN") {
		t.Fatalf("error response = %d %s", w.Code, w.Body.String())
	}

	missingRepo := &fakeAsyncRepo{err: storage.ErrNotFound}
	r = httptest.NewRequest(http.MethodGet, "/api/async/missing", nil)
	r.SetPathValue("id", "missing")
	w = httptest.NewRecorder()
	NewHandler(missingRepo).Show(w, r)
	if w.Code != http.StatusNotFound || !containsJSONField(t, w.Body.Bytes(), "code", "RESOURCE_NOT_FOUND") {
		t.Fatalf("missing response = %d %s", w.Code, w.Body.String())
	}

	brokenRepo := &fakeAsyncRepo{err: errors.New("database offline")}
	r = httptest.NewRequest(http.MethodGet, "/api/async/broken", nil)
	r.SetPathValue("id", "broken")
	w = httptest.NewRecorder()
	NewHandler(brokenRepo).Show(w, r)
	if w.Code != http.StatusInternalServerError || !containsJSONField(t, w.Body.Bytes(), "code", "INTERNAL_ERROR") {
		t.Fatalf("server error response = %d %s", w.Code, w.Body.String())
	}
}

func containsJSONField(t *testing.T, raw []byte, field string, value string) bool {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return findJSONField(decoded, field, value)
}

func findJSONField(value any, field string, want string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == field && child == want {
				return true
			}
			if findJSONField(child, field, want) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if findJSONField(child, field, want) {
				return true
			}
		}
	}
	return false
}

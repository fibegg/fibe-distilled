package launch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	playgroundpkg "github.com/fibegg/fibe-distilled/internal/playground"
)

func TestWriteCreatePlaygroundErrPreservesResponseShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
		details map[string]any
	}{
		{
			name:    "structured api error",
			err:     APIError(http.StatusNotImplemented, "NOT_IMPLEMENTED", "unsupported launch dependency", map[string]any{"surface": "launch"}),
			status:  http.StatusNotImplemented,
			code:    "NOT_IMPLEMENTED",
			message: "unsupported launch dependency",
			details: map[string]any{"surface": "launch"},
		},
		{
			name:    "bad request",
			err:     BadRequestError("launch payload is invalid"),
			status:  http.StatusBadRequest,
			code:    "BAD_REQUEST",
			message: "launch payload is invalid",
		},
		{
			name:    "conflict",
			err:     ConflictError("launch playground name already exists"),
			status:  http.StatusConflict,
			code:    "RESOURCE_IN_USE",
			message: "launch playground name already exists",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/launches", nil)
			writeCreatePlaygroundErr(rec, req, tt.err)
			if rec.Code != tt.status {
				t.Fatalf("status = %d; want %d", rec.Code, tt.status)
			}
			var body map[string]map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			got := body["error"]
			if got["code"] != tt.code || got["message"] != tt.message {
				t.Fatalf("error = %#v; want code %q message %q", got, tt.code, tt.message)
			}
			if tt.details != nil {
				details, ok := got["details"].(map[string]any)
				if !ok {
					t.Fatalf("details = %#v; want object", got["details"])
				}
				for key, want := range tt.details {
					if details[key] != want {
						t.Fatalf("details[%q] = %#v; want %#v", key, details[key], want)
					}
				}
			}
		})
	}
}

func TestDeployRuntimePlaygroundRequiresConfiguredDeployer(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, Options{})
	_, err := handler.deployRuntimePlayground(context.Background(), playgroundpkg.CreatePayload{}, false)
	if err == nil || err.Error() != "launch playground deployer is not configured" {
		t.Fatalf("deployRuntimePlayground error = %v; want missing deployer error", err)
	}
}

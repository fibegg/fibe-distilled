package async

import (
	"context"
	"errors"
	"maps"
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/api/request"
	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/storage"
)

// Repository loads async operation state.
type Repository interface {
	// GetAsync loads one async operation by request ID.
	GetAsync(context.Context, string) (domain.AsyncOperation, error)
}

// Handler serves async operation polling endpoints.
type Handler struct {
	repo Repository
}

// NewHandler constructs an async polling handler.
func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

// Show returns async operation state in SDK-compatible shape.
func (h Handler) Show(w http.ResponseWriter, r *http.Request) {
	op, err := h.repo.GetAsync(r.Context(), request.PathValue(r, "id"))
	if errors.Is(err, storage.ErrNotFound) {
		response.NotFound(w, r, "async request")
		return
	}
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusOK, responseBody(op))
}

// responseBody returns the SDK polling shape for an async operation.
func responseBody(op domain.AsyncOperation) map[string]any {
	switch op.Status {
	case domain.AsyncError:
		return errorBody(op)
	case domain.AsyncSuccess:
		return successBody(op)
	default:
		return pendingBody(op)
	}
}

// errorBody returns a completed async error response.
func errorBody(op domain.AsyncOperation) map[string]any {
	body := map[string]any{"request_id": op.ID, "status": "error"}
	if op.Error != nil {
		body["error"] = op.Error.Message
		body["error_code"] = op.Error.Code
		body["error_details"] = op.Error.Details
		body["error_status"] = http.StatusUnprocessableEntity
	}
	return body
}

// successBody returns a completed async success response.
func successBody(op domain.AsyncOperation) map[string]any {
	body := map[string]any{"request_id": op.ID}
	maps.Copy(body, op.Payload)
	if _, ok := body["status"]; !ok {
		body["status"] = "success"
	}
	return body
}

// pendingBody returns an in-flight async response.
func pendingBody(op domain.AsyncOperation) map[string]any {
	return map[string]any{"request_id": op.ID, "status": op.Status, "status_url": op.StatusURL}
}

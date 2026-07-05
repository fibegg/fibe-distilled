package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// listMeta is the Fibe-compatible pagination metadata for list responses.
type listMeta struct {
	// Page is the current one-based page number.
	Page int `json:"page"`
	// PerPage is the requested page size.
	PerPage int `json:"per_page"`
	// Total is the total count before pagination.
	Total int `json:"total"`
}

// listEnvelope wraps list data in the API's {data, meta} shape.
type listEnvelope[T any] struct {
	// Data is the page of resource records.
	Data []T `json:"data"`
	// Meta carries Fibe-compatible pagination metadata.
	Meta listMeta `json:"meta"`
}

// JSON writes a JSON response with a request ID header.
func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-Id", RequestID(r))
	if v == nil {
		w.WriteHeader(status)
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body, err = json.Marshal(map[string]any{
			"error": domain.APIError{
				Code:    "INTERNAL_ERROR",
				Message: fmt.Sprintf("encode response: %v", err),
			},
		})
		if err != nil {
			body = []byte(`{"error":{"code":"INTERNAL_ERROR","message":"encode response failed"}}`)
		}
	}
	w.WriteHeader(status)
	if _, err := w.Write(append(body, '\n')); err != nil {
		return
	}
}

// NoContent writes a 204 response with a request ID header.
func NoContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Request-Id", RequestID(r))
	w.WriteHeader(http.StatusNoContent)
}

// List writes a paginated list envelope for in-memory result slices.
func List[T any](w http.ResponseWriter, r *http.Request, data []T) {
	pagination, err := listPagination(r)
	if err != nil {
		BadRequest(w, r, err.Error())
		return
	}
	total := len(data)
	paged := data
	if pagination.perPage > 0 {
		start := (pagination.page - 1) * pagination.perPage
		switch {
		case start >= total:
			paged = data[:0]
		default:
			end := start + pagination.perPage
			end = min(end, total)
			paged = data[start:end]
		}
	}
	JSON(w, r, http.StatusOK, listEnvelope[T]{
		Data: paged,
		Meta: listMeta{Page: pagination.page, PerPage: pagination.perPage, Total: total},
	})
}

// Error writes a structured API error response.
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	JSON(w, r, status, map[string]any{
		"error": domain.APIError{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// BadRequest writes a BAD_REQUEST response.
func BadRequest(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusBadRequest, "BAD_REQUEST", message, nil)
}

// Unauthorized writes the static bearer-token auth error.
func Unauthorized(w http.ResponseWriter, r *http.Request) {
	Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "valid bearer token required", nil)
}

// NotFound writes a RESOURCE_NOT_FOUND response for a resource type.
func NotFound(w http.ResponseWriter, r *http.Request, resource string) {
	Error(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", resource+" not found", nil)
}

// NotImplemented writes fibe-distilled's stable unsupported-surface response.
func NotImplemented(w http.ResponseWriter, r *http.Request, surface string) {
	Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", surface+" is outside fibe-distilled minimal scope", nil)
}

// ServerError writes an INTERNAL_ERROR response.
func ServerError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		err = errors.New("unknown error")
	}
	Error(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}

// ValidationError is implemented by resource-private errors with structured API shape.
type ValidationError interface {
	error
	// ResponseStatus returns the HTTP status for the validation error.
	ResponseStatus() int
	// ResponseCode returns the client-visible error code.
	ResponseCode() string
	// ResponseDetails returns optional structured error details.
	ResponseDetails() map[string]any
}

// BadRequestError is implemented by resource-private bad-request errors.
type BadRequestError interface {
	error
	// BadRequestMessage returns the client-visible BAD_REQUEST message.
	BadRequestMessage() string
}

// ConflictError is implemented by resource-private conflict errors.
type ConflictError interface {
	error
	// ConflictMessage returns the client-visible conflict message.
	ConflictMessage() string
}

// WriteValidationError writes structured validation errors when present.
func WriteValidationError(w http.ResponseWriter, r *http.Request, err error) bool {
	var validation ValidationError
	if !errors.As(err, &validation) {
		return false
	}
	status := validation.ResponseStatus()
	if status == 0 {
		status = http.StatusUnprocessableEntity
	}
	Error(w, r, status, validation.ResponseCode(), validation.Error(), validation.ResponseDetails())
	return true
}

// ValidationOrBadRequest writes structured validation errors or BAD_REQUEST.
func ValidationOrBadRequest(w http.ResponseWriter, r *http.Request, err error) {
	if WriteValidationError(w, r, err) {
		return
	}
	BadRequest(w, r, err.Error())
}

// ValidationOrServerError writes structured validation errors or INTERNAL_ERROR.
func ValidationOrServerError(w http.ResponseWriter, r *http.Request, err error) {
	if WriteValidationError(w, r, err) {
		return
	}
	ServerError(w, r, err)
}

// CreateDependencyError writes SDK-shaped create/deploy dependency failures.
func CreateDependencyError(w http.ResponseWriter, r *http.Request, err error, dependency string, notFound func(error) bool) {
	if conflict, ok := errors.AsType[ConflictError](err); ok {
		Error(w, r, http.StatusConflict, "RESOURCE_IN_USE", conflict.ConflictMessage(), nil)
		return
	}
	if WriteValidationError(w, r, err) {
		return
	}
	if badRequest, ok := errors.AsType[BadRequestError](err); ok {
		BadRequest(w, r, badRequest.BadRequestMessage())
		return
	}
	if notFound != nil && notFound(err) {
		NotFound(w, r, dependency)
		return
	}
	ServerError(w, r, err)
}

// requestIDKey stores the request ID in request context.
type requestIDKey struct{}

// WithRequestID returns a request carrying a response request ID.
func WithRequestID(r *http.Request, id string) *http.Request {
	ctx := r.Context()
	ctx = context.WithValue(ctx, requestIDKey{}, id)
	return r.WithContext(ctx)
}

// RequestID returns the request ID associated with a request.
func RequestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDKey{}).(string); ok && id != "" {
		return id
	}
	return "req_missing"
}

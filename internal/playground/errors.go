package playground

import (
	"errors"
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// writeStoreErr maps store not-found errors to resource 404 responses.
func writeStoreErr(w http.ResponseWriter, r *http.Request, resource string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		response.NotFound(w, r, resource)
		return
	}
	response.ServerError(w, r, err)
}

// writePayloadErr maps payload validation errors to API responses.
func writePayloadErr(w http.ResponseWriter, r *http.Request, err error) {
	response.ValidationOrBadRequest(w, r, err)
}

// writeCreatePlaygroundErr maps create/deploy errors to SDK-shaped responses.
func writeCreatePlaygroundErr(w http.ResponseWriter, r *http.Request, err error) {
	response.CreateDependencyError(w, r, err, "playground dependency", isStoreNotFound)
}

// validationError creates the default 422 validation error.
func validationError(message string) apiValidationError {
	return apiValidationError{code: "VALIDATION_ERROR", message: message}
}

// conflictError represents a Playground resource conflict.
type conflictError struct {
	message string
}

// Error returns the conflict message.
func (e conflictError) Error() string {
	return e.message
}

// ConflictMessage returns the client-visible conflict message.
func (e conflictError) ConflictMessage() string {
	return e.message
}

// apiValidationError carries a structured Playground API error response.
type apiValidationError struct {
	status  int
	code    string
	message string
	details map[string]any
}

// Error returns the validation message.
func (e apiValidationError) Error() string {
	return e.message
}

// ResponseStatus returns the HTTP status used for structured validation errors.
func (e apiValidationError) ResponseStatus() int {
	return e.status
}

// ResponseCode returns the client-visible validation code.
func (e apiValidationError) ResponseCode() string {
	return e.code
}

// ResponseDetails returns structured validation details.
func (e apiValidationError) ResponseDetails() map[string]any {
	return e.details
}

// badRequestError carries a Playground bad-request message.
type badRequestError struct {
	message string
}

// Error returns the bad-request message.
func (e badRequestError) Error() string {
	return e.message
}

// BadRequestMessage returns the client-visible BAD_REQUEST message.
func (e badRequestError) BadRequestMessage() string {
	return e.message
}

// isStoreNotFound reports storage not-found dependency failures.
func isStoreNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}

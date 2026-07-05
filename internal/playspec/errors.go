package playspec

import (
	"errors"
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// payloadError wraps payload validation errors from apply.
type payloadError struct {
	err error
}

// Error returns the wrapped Playspec payload failure message.
func (e payloadError) Error() string {
	return e.err.Error()
}

// Unwrap returns the wrapped Playspec payload failure.
func (e payloadError) Unwrap() error {
	return e.err
}

// writeUpdateErr maps update failures to API responses.
func writeUpdateErr(w http.ResponseWriter, r *http.Request, err error) {
	if payloadErr, ok := errors.AsType[payloadError](err); ok {
		writePayloadErr(w, r, payloadErr.err)
		return
	}
	writeStoreErr(w, r, "playspec", err)
}

// writePayloadErr maps payload validation errors to API responses.
func writePayloadErr(w http.ResponseWriter, r *http.Request, err error) {
	response.ValidationOrBadRequest(w, r, err)
}

// writeStoreErr maps storage not-found errors to resource responses.
func writeStoreErr(w http.ResponseWriter, r *http.Request, resource string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		response.NotFound(w, r, resource)
		return
	}
	response.ServerError(w, r, err)
}

// apiValidationError carries a structured API validation response.
type apiValidationError struct {
	status  int
	code    string
	message string
	details map[string]any
}

// Error returns the validation message used in API responses.
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

// badRequestError represents a 400-level payload or query issue.
type badRequestError struct {
	message string
}

// Error returns the bad-request message used in API responses.
func (e badRequestError) Error() string {
	return e.message
}

// BadRequestMessage returns the client-visible BAD_REQUEST message.
func (e badRequestError) BadRequestMessage() string {
	return e.message
}

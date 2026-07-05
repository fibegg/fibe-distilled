package launch

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/request"
	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/git"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// jsonFields aliases shared JSON field-presence tracking.
type jsonFields = request.JSONFields

// presentBlankString reports a present string field that is blank.
func presentBlankString(fields jsonFields, name string, value string) bool {
	return fields.Has(name) && strings.TrimSpace(value) == ""
}

// hasBlankServiceSubdomains reports blank keys or values in service_subdomains.
func hasBlankServiceSubdomains(values map[string]string) bool {
	for service, subdomain := range values {
		if strings.TrimSpace(service) == "" || strings.TrimSpace(subdomain) == "" {
			return true
		}
	}
	return false
}

// hasTrimmedStringMapKeyCollision reports keys that collide after trimming.
func hasTrimmedStringMapKeyCollision(values map[string]string) bool {
	seen := map[string]bool{}
	for key := range values {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if seen[trimmed] {
			return true
		}
		seen[trimmed] = true
	}
	return false
}

// blankJSONReference reports explicit blank string references.
func blankJSONReference(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return strings.TrimSpace(value) == ""
}

// idReference carries a parsed numeric ID or named resource reference.
type idReference struct {
	id         *int64
	identifier string
}

// parseIDReference accepts positive integer IDs or nonblank names.
func parseIDReference(raw json.RawMessage, field string) (idReference, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return idReference{}, nil
	}
	var numeric int64
	if err := json.Unmarshal(raw, &numeric); err == nil {
		if numeric <= 0 {
			return idReference{}, badRequestError{message: field + " must be a positive id or name"}
		}
		return idReference{id: &numeric}, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return idReference{}, nil
		}
		return idReference{identifier: text}, nil
	}
	return idReference{}, badRequestError{message: field + " must be a positive id or name"}
}

// normalizeRepositoryURLInput trims transport noise while preserving URL spelling.
func normalizeRepositoryURLInput(raw string) string {
	return normalizeScalarInput(raw)
}

// normalizeScalarInput trims surrounding whitespace from accepted text fields.
func normalizeScalarInput(raw string) string {
	return strings.TrimSpace(raw)
}

// validateRuntimeRepositoryURL rejects repository targets that runtime Git cannot clone.
func validateRuntimeRepositoryURL(raw string) error {
	if err := validateRepositoryURLNoCredentials(raw); err != nil {
		return err
	}
	if !git.CloneableRepositoryURL(raw) {
		return badRequestError{message: "repository_url must be a cloneable Git URL or SSH target"}
	}
	return nil
}

// validateRepositoryURLNoCredentials rejects embedded Git credentials.
func validateRepositoryURLNoCredentials(raw string) error {
	if git.RepositoryURLHasCredentials(raw) {
		return badRequestError{message: "repository_url must not include credentials; use process credentials or SSH access"}
	}
	return nil
}

// idString formats positive persisted IDs for name-or-ID store lookups.
func idString(id int64) string {
	return strconv.FormatInt(id, 10)
}

// ignoreNotFound treats already-cleaned resources as successful cleanup.
func ignoreNotFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

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

// writeRuntimeWritableErr maps repository writability errors.
func writeRuntimeWritableErr(w http.ResponseWriter, r *http.Request, err error) {
	response.ValidationOrServerError(w, r, err)
}

// writeCreatePlaygroundErr maps create/deploy errors to SDK-shaped responses.
func writeCreatePlaygroundErr(w http.ResponseWriter, r *http.Request, err error) {
	response.CreateDependencyError(w, r, err, "playground dependency", isStoreNotFound)
}

// endpointError translates neighboring resource errors into Launch-owned errors.
func endpointError(err error) error {
	if err == nil {
		return nil
	}
	if validation, ok := errors.AsType[response.ValidationError](err); ok {
		return APIError(validation.ResponseStatus(), validation.ResponseCode(), validation.Error(), validation.ResponseDetails())
	}
	if badRequest, ok := errors.AsType[response.BadRequestError](err); ok {
		return BadRequestError(badRequest.BadRequestMessage())
	}
	if conflict, ok := errors.AsType[response.ConflictError](err); ok {
		return ConflictError(conflict.ConflictMessage())
	}
	return err
}

// APIError adapts structured API errors from neighboring services into Launch responses.
func APIError(status int, code string, message string, details map[string]any) error {
	return apiValidationError{status: status, code: code, message: message, details: details}
}

// BadRequestError adapts bad-request errors from neighboring services into Launch responses.
func BadRequestError(message string) error {
	return badRequestError{message: message}
}

// ConflictError adapts resource conflicts from neighboring services into Launch responses.
func ConflictError(message string) error {
	return conflictError{message: message}
}

// conflictError represents a Launch resource conflict.
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

// apiValidationError carries a structured Launch API error response.
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

// badRequestError carries a Launch bad-request message.
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

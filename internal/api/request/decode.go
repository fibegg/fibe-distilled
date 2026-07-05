package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/go-chi/chi/v5"
)

// Decode decodes one required JSON request body and writes a bad request on failure.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeRequestJSON(w, r, dst, false)
}

// DecodeOptional decodes a JSON body that may be empty.
func DecodeOptional(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeRequestJSON(w, r, dst, true)
}

// PathValue reads path params from stdlib or chi routing.
func PathValue(r *http.Request, key string) string {
	if value := r.PathValue(key); value != "" {
		return value
	}
	return chi.URLParam(r, key)
}

// decodeRequestJSON decodes and writes a bad request response on failure.
func decodeRequestJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) bool {
	defer func() { _ = r.Body.Close() }()
	if err := decodeSingleJSONValue(r.Body, dst, allowEmpty); err != nil {
		response.BadRequest(w, r, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// decodeSingleJSONValue rejects null and trailing JSON values.
func decodeSingleJSONValue(body io.Reader, dst any, allowEmpty bool) error {
	raw, err := singleJSONRawMessage(body, allowEmpty)
	if err != nil || raw == nil {
		return err
	}
	if rawMessageIsNull(raw) {
		return errors.New("top-level JSON null is not a valid request body")
	}
	return json.Unmarshal(raw, dst)
}

// singleJSONRawMessage reads exactly one top-level JSON value.
func singleJSONRawMessage(body io.Reader, allowEmpty bool) (json.RawMessage, error) {
	decoder := json.NewDecoder(body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		if allowEmpty && err == io.EOF {
			return nil, nil
		}
		return nil, err
	}
	return raw, rejectTrailingJSONValue(decoder)
}

// rejectTrailingJSONValue rejects any second JSON value or trailing garbage.
func rejectTrailingJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// rawMessageIsNull reports whether the top-level JSON value is null.
func rawMessageIsNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

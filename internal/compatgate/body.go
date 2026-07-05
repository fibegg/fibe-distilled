package compatgate

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// maxJSONBodyBytes is the largest body the gate will fully inspect.
const maxJSONBodyBytes int64 = 16 << 20

// bodyInspection is the gate's JSON-body classification result.
type bodyInspection struct {
	body     map[string]any
	parsed   bool
	tooLarge bool
}

// inspectableJSONBody reads a JSON body for classification and restores it.
func inspectableJSONBody(r *http.Request) bodyInspection {
	if r.Body == nil || !methodMayHaveBody(r.Method) {
		return bodyInspection{}
	}
	if r.ContentLength > maxJSONBodyBytes {
		return bodyInspection{tooLarge: true}
	}
	raw, err := readAndRestoreBody(r)
	if err != nil || len(raw) == 0 {
		return bodyInspection{}
	}
	if int64(len(raw)) > maxJSONBodyBytes {
		return bodyInspection{tooLarge: true}
	}
	body, ok := decodeJSONMap(raw)
	return bodyInspection{body: body, parsed: ok}
}

// readAndRestoreBody reads the inspection prefix and restores the request body.
func readAndRestoreBody(r *http.Request) ([]byte, error) {
	original := r.Body
	raw, err := io.ReadAll(io.LimitReader(original, maxJSONBodyBytes+1))
	// Always restore the FULL body for the downstream handler by chaining the bytes
	// we already read with whatever is still unread. If ContentLength understated the
	// real size, this avoids handing the handler a truncated body.
	r.Body = restoredBody(raw, original)
	return raw, err
}

// decodeJSONMap parses a single JSON object while preserving JSON numbers.
func decodeJSONMap(raw []byte) (map[string]any, bool) {
	var body map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return nil, false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, false
	}
	return body, true
}

// methodMayHaveBody reports methods whose JSON body may affect compatibility.
func methodMayHaveBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

// restoredBody replays bytes already read and then the original stream.
func restoredBody(prefix []byte, original io.ReadCloser) io.ReadCloser {
	return restoreCloser{
		Reader: io.MultiReader(bytes.NewReader(prefix), original),
		closer: original,
	}
}

// restoreCloser preserves the original closer while replaying a body.
type restoreCloser struct {
	io.Reader
	closer io.Closer
}

// Close closes the original request body after replay.
func (b restoreCloser) Close() error {
	return b.closer.Close()
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

type playgroundExpirationExtension struct {
	ok bool
}

func (s *Server) extendPlaygroundExpiration(w http.ResponseWriter, r *http.Request, pg domain.Playground, duration time.Duration) playgroundExpirationExtension {
	body := map[string]any{"duration_hours": int(duration / time.Hour)}
	req := playgroundTestRequest(r, pg, body)
	s.playground.Expiration(w, req)
	return playgroundExpirationExtension{ok: testResponseOK(w)}
}

func (s *Server) playgroundLogsPayload(ctx context.Context, pg domain.Playground, service string, tail int) (map[string]any, *domain.APIError) {
	return s.playground.LogsPayload(ctx, pg, service, tail)
}

func (s *Server) applyPlaygroundOperation(w http.ResponseWriter, r *http.Request, pg domain.Playground, action string) (domain.Playground, bool) {
	return s.runPlaygroundOperation(w, r, pg, action)
}

func (s *Server) deployPlaygroundOperation(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	return s.runPlaygroundOperation(w, r, pg, "rollout")
}

func (s *Server) downExistingComposeForRestart(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	return s.runPlaygroundOperation(w, r, pg, "hard_restart")
}

func (s *Server) hardRestartPlayground(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	return s.runPlaygroundOperation(w, r, pg, "hard_restart")
}

func (s *Server) runPlaygroundOperation(w http.ResponseWriter, r *http.Request, pg domain.Playground, action string) (domain.Playground, bool) {
	req := playgroundTestRequest(r, pg, map[string]any{"action_type": action})
	s.playground.Operations(w, req)
	ok := waitPlaygroundOperationAdapterAsync(req.Context(), s, w)
	current, err := s.store.GetPlayground(req.Context(), strconv.FormatInt(pg.ID, 10))
	if err != nil {
		return pg, false
	}
	return current, ok
}

func playgroundTestRequest(r *http.Request, pg domain.Playground, body map[string]any) *http.Request {
	data, _ := json.Marshal(body)
	req := r.Clone(r.Context())
	req.Body = http.NoBody
	if body != nil {
		req.Body = ioNopCloser{Reader: bytes.NewReader(data)}
		req.Header = req.Header.Clone()
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetPathValue("identifier", pg.Name)
	return req
}

func testResponseOK(w http.ResponseWriter) bool {
	if rec, ok := w.(interface{ Result() *http.Response }); ok {
		code := rec.Result().StatusCode
		return code >= 200 && code < 300
	}
	return true
}

func waitPlaygroundOperationAdapterAsync(ctx context.Context, s *Server, w http.ResponseWriter) bool {
	rec, ok := w.(*httptest.ResponseRecorder)
	if !ok || rec.Code != http.StatusAccepted {
		return testResponseOK(w)
	}
	var queued map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &queued); err != nil {
		return false
	}
	requestID, _ := queued["request_id"].(string)
	if requestID == "" {
		return false
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op, err := s.store.GetAsync(ctx, requestID)
		if err != nil {
			return false
		}
		switch op.Status {
		case domain.AsyncSuccess:
			return true
		case domain.AsyncError:
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

type ioNopCloser struct {
	*bytes.Reader
}

func (c ioNopCloser) Close() error {
	return nil
}

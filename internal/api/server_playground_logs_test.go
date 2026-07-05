package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"github.com/fibegg/fibe-distilled/internal/worker"
)

func TestPlaygroundLogsReturnsAsyncErrorForDockerLogFailure(t *testing.T) {
	ctx := context.Background()
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{"FIBE_DISTILLED_LOGS": errors.New("docker logs failed")},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_LOGS": {Stderr: "no such service: web"},
		},
	}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()

	createRunningLogsPlayground(ctx, t, st, "log-failure-pg", "log-failure--1")

	var queued map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/log-failure-pg/logs", map[string]any{"service": "web"}, "test-token")
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.StatusCode)
	}
	decodeResp(t, res, &queued)

	final := waitAsyncError(t, srv, queued["status_url"].(string))
	if final["error_code"] != "LOGS_UNAVAILABLE" {
		t.Fatalf("expected LOGS_UNAVAILABLE async error, got %#v", final)
	}
	if !strings.Contains(final["error"].(string), "no such service: web") {
		t.Fatalf("expected docker stderr in async error, got %#v", final)
	}
}

func TestPlaygroundLogsUseCurrentRowBeforeRemoteCommand(t *testing.T) {
	fixture := newCurrentRowLogsFixture(t)

	payload, apiErr := fixture.app.playgroundLogsPayload(fixture.ctx, fixture.playground, "web", 1)
	if apiErr != nil {
		t.Fatalf("logs payload failed: %#v", apiErr)
	}
	assertCurrentRowLogsPayload(t, payload, fixture.fake)
}

func TestPlaygroundLogsValidateServiceAgainstCurrentRow(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := &runtimetest.FakeExecutor{}
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: fake}})
	pg := createRunningLogsPlayground(ctx, t, st, "logs-service-pg", "logs-service--1")
	current := pg
	current.Services = nil
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save current services: %v", err)
	}

	payload, apiErr := app.playgroundLogsPayload(ctx, pg, "web", 1)
	if apiErr == nil || apiErr.Code != "SERVICE_NOT_FOUND" {
		t.Fatalf("expected SERVICE_NOT_FOUND, payload=%#v err=%#v", payload, apiErr)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("service mismatch should not call runtime logs, saw:\n%s", strings.Join(fake.Seen, "\n"))
	}
}

func TestPlaygroundLogsPreflightAllowsServiceFromCurrentRow(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: &runtimetest.FakeExecutor{}}})
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:   "logs-preflight-pg",
		Status: domain.StatusRunning,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	current := pg
	current.Services = []domain.PlaygroundServiceInfo{{Name: "web", Status: "running", Running: true}}
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save current services: %v", err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/logs-preflight-pg/logs", map[string]any{"service": "web"}, "test-token")
	assertStatus(t, res, http.StatusAccepted, "logs preflight should use current service list")
	closeResponseBody(t, res)
}

func TestPlaygroundLogsReturnsAllLiveServiceLogsWhenServiceOmitted(t *testing.T) {
	srv, fake := newAllLiveLogsServer(t)
	final := requestPlaygroundLogs(t, srv, "/api/playgrounds/all-live-logs/logs", map[string]any{"tail": 2})
	assertAggregateLiveLogs(t, final)
	assertSeenCommands(t, fake, []string{"FIBE_DISTILLED_LOGS all-live-logs--1", " web 2", " worker 2"})
}

func TestPlaygroundLogsUseServiceURLsWhenRuntimeServicesMissing(t *testing.T) {
	ctx := context.Background()
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_LOGS url-only-logs--1 /opt/fibe/playgrounds/url-only-logs--1 web 1": {Stdout: "url-service-line\n"},
		},
	}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()

	mq := ensureTestConfiguredMarquee(t, st)
	project := "url-only-logs--1"
	if _, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "url-only-logs",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		ServiceURLs:    []domain.PlaygroundServiceURL{{Name: "web", URL: "https://web.example.test"}},
	}); err != nil {
		t.Fatalf("create playground: %v", err)
	}

	var queued map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/url-only-logs/logs", map[string]any{"service": " web ", "tail": 1}, "test-token")
	decodeResp(t, res, &queued)
	final := waitAsyncSuccess(t, srv, queued["status_url"].(string))
	if final["service"] != "web" || final["lines"].([]any)[0] != "url-service-line" {
		t.Fatalf("expected trimmed service-specific URL-backed logs, got %#v", final)
	}

	res = doReq(t, srv, http.MethodPost, "/api/playgrounds/url-only-logs/logs", map[string]any{"tail": 1}, "test-token")
	decodeResp(t, res, &queued)
	final = waitAsyncSuccess(t, srv, queued["status_url"].(string))
	if final["source"] != "docker" || final["lines"].([]any)[0] != "[web] url-service-line" {
		t.Fatalf("expected aggregate URL-backed logs, got %#v", final)
	}
}

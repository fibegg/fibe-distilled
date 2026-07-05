package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"github.com/fibegg/fibe-distilled/internal/worker"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

func assertRuntimeConfigUpdateMarksHasChanges(t *testing.T, ctx context.Context, st *store.DB, srv *httptest.Server, status string) {
	t.Helper()
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:   "runtime-update-" + strings.ReplaceAll(status, "_", "-"),
		Status: status,
	})
	if err != nil {
		t.Fatalf("create %s playground: %v", status, err)
	}
	res := doReq(t, srv, http.MethodPatch, "/api/playgrounds/"+pg.Name, map[string]any{"playground": map[string]any{"env_overrides": map[string]any{"NEW": "2"}}}, "test-token")
	assertStatus(t, res, http.StatusOK, "runtime config update")
	closeResponseBody(t, res)
	updated, err := st.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		t.Fatalf("reload %s playground: %v", status, err)
	}
	if updated.Status != domain.StatusHasChanges {
		t.Fatalf("runtime config update from %s should mark has_changes, got %#v", status, updated)
	}
	if updated.StateReason == nil || *updated.StateReason != "playground_config_changed" {
		t.Fatalf("runtime config update should record config-change reason, got %#v", updated.StateReason)
	}
}

func assertRenderedEnv(t *testing.T, composeYAML string, service string, want map[string]string) {
	t.Helper()
	var rendered map[string]any
	if err := yaml.Unmarshal([]byte(composeYAML), &rendered); err != nil {
		t.Fatalf("parse rendered compose: %v\n%s", err, composeYAML)
	}
	services := rendered["services"].(map[string]any)
	serviceConfig := services[service].(map[string]any)
	env := serviceConfig["environment"].(map[string]any)
	for key, wantValue := range want {
		if got := env[key]; got != wantValue {
			t.Fatalf("rendered env %s = %#v, want %#v in %s", key, got, wantValue, composeYAML)
		}
	}
}

func failingRemoteDeployExecutor() *runtimetest.FakeExecutor {
	return &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"docker compose -f compose.yml": errors.New("local deploy still running"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"docker compose -f compose.yml": {Stderr: "local deploy still running"},
		},
	}
}

func createRepositoryLaunchMarquee(t *testing.T, st *store.DB) domain.Marquee {
	t.Helper()
	domains := "repo-launch.example.test"
	return ensureTestConfiguredMarqueeWith(t, st, domain.Marquee{DomainsInput: &domains})
}

func createRepositoryLaunch(t *testing.T, srv *httptest.Server, marqueeID int64) map[string]any {
	t.Helper()
	var launch map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":           "repo-async-launch",
		"repository_url": "ssh://git.example.test/acme/repo-async-launch.git",
		"marquee_id":     marqueeID,
		"compose_yaml": `services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
`,
	}, "test-token")
	if res.StatusCode != http.StatusCreated {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("repository launch should return IDs before local deploy failure, got %d: %#v", res.StatusCode, got)
	}
	decodeResp(t, res, &launch)
	if numberID(launch["playspec_id"]) == "" || numberID(launch["playground_id"]) == "" {
		t.Fatalf("expected launch IDs, got %#v", launch)
	}
	return launch
}

func waitForAsyncLaunchError(t *testing.T, st *store.DB, playgroundID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		pg, err := st.GetPlayground(context.Background(), playgroundID)
		if err != nil {
			t.Fatalf("get async launch playground: %v", err)
		}
		if pg.Status == domain.StatusError {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected async deployment failure to persist, got %s", pg.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newReservedOverrideHandlerApp(t *testing.T) (*Server, int64, string) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mq := ensureTestConfiguredMarquee(t, st)
	ps, err := st.CreatePlayspec(context.Background(), domain.Playspec{
		Name:            "reserved-run-spec",
		BaseComposeYAML: "services:\n  web:\n    image: alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(context.Background(), domain.Playground{
		Name:       "reserved-run-pg",
		PlayspecID: ps.ID,
		MarqueeID:  &mq.ID,
		Status:     domain.StatusRunning,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return New(config.Config{APIToken: "test-token"}, st, worker.Worker{
		DB: st,
		Runtime: runtime.Checker{
			Executor: &runtimetest.FakeExecutor{},
		},
	}), *ps.ID, pg.Name
}

func reservedServiceOverrideCases(playspecID int64, playgroundName string) []serviceOverrideCase {
	return []serviceOverrideCase{
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/api/playgrounds",
			body:   fmt.Sprintf(`{"playground":{"name":"reserved-run-create-pg","playspec_id":%d,"services":{"_run":{"only_services":["web"]}}}}`, playspecID),
		},
		{
			name:   "update",
			method: http.MethodPatch,
			path:   "/api/playgrounds/" + playgroundName,
			body:   `{"playground":{"services":{"_run":{"only_services":["web"]}}}}`,
		},
		{
			name:   "fixture marker create",
			method: http.MethodPost,
			path:   "/api/playgrounds",
			body:   fmt.Sprintf(`{"playground":{"name":"fixture-marker-create-pg","playspec_id":%d,"services":{"web":{"fixture_marker":"sdk-repo-api"}}}}`, playspecID),
		},
		{
			name:   "fixture marker update",
			method: http.MethodPatch,
			path:   "/api/playgrounds/" + playgroundName,
			body:   `{"playground":{"services":{"web":{"fixture_marker":"sdk-repo-api"}}}}`,
		},
	}
}

func assertServiceOverrideRejectedByHandler(t *testing.T, app *Server, method string, path string, body string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("handler should reject reserved service override without relying on compatgate, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode reserved override error: %v", err)
	}
	if payload["error"].(map[string]any)["code"] != "INVALID_SERVICE_OVERRIDES" {
		t.Fatalf("unexpected reserved override error: %#v", payload)
	}
}

func assertRenderedServiceAuthPassword(t *testing.T, rendered string, playgroundID string, servicePassword string, globalPassword string) {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v", err)
	}
	services := doc["services"].(map[string]any)
	web := services["web"].(map[string]any)
	labels := web["labels"].(map[string]any)
	var users string
	for key, raw := range labels {
		if strings.HasSuffix(key, ".basicauth.users") {
			users, _ = raw.(string)
			break
		}
	}
	if users == "" {
		t.Fatalf("expected internal service auth middleware labels: %#v", labels)
	}
	hash := strings.TrimPrefix(users, "playground:")
	hash = strings.TrimSuffix(hash, "-"+playgroundID)
	hash = strings.ReplaceAll(hash, "$$", "$")
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(servicePassword)); err != nil {
		t.Fatalf("auth hash should match service auth_password: %v\nusers=%s", err, users)
	}
	if globalPassword != "" {
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(globalPassword)); err == nil {
			t.Fatalf("auth hash unexpectedly matched global internal password")
		}
	}
}

func stringFromMap(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func seedResourceListFilterRows(t *testing.T, ctx context.Context, st *store.DB) {
	t.Helper()
	if _, err := st.CreateProp(ctx, domain.Prop{Name: "prop-filter-public", RepositoryURL: "https://github.com/acme/public.git", Provider: "github", Status: "active"}); err != nil {
		t.Fatalf("create public prop: %v", err)
	}
	if _, err := st.CreateProp(ctx, domain.Prop{Name: "prop-filter-private", RepositoryURL: "ssh://git.example.test/acme/private.git", Provider: "git", Private: true, Status: "archived"}); err != nil {
		t.Fatalf("create private prop: %v", err)
	}
	if _, err := st.CreatePlayspec(ctx, domain.Playspec{Name: "playspec-filter-plain", BaseComposeYAML: "services:\n  job:\n    image: alpine\n"}); err != nil {
		t.Fatalf("create plain playspec: %v", err)
	}
	locked, err := st.CreatePlayspec(ctx, domain.Playspec{Name: "playspec-filter-locked", BaseComposeYAML: "services:\n  web:\n    image: alpine\n"})
	if err != nil {
		t.Fatalf("create locked playspec: %v", err)
	}
	if _, err := st.CreatePlayground(ctx, domain.Playground{Name: "lock-holder", Status: domain.StatusRunning, PlayspecID: locked.ID}); err != nil {
		t.Fatalf("create lock holder: %v", err)
	}
	if _, err := st.CreatePlayground(ctx, domain.Playground{Name: "playground-filter-running", Status: domain.StatusRunning, PlayspecID: locked.ID}); err != nil {
		t.Fatalf("create running playground: %v", err)
	}
	if _, err := st.CreatePlayground(ctx, domain.Playground{Name: "playground-filter-error", Status: domain.StatusError, PlayspecID: locked.ID}); err != nil {
		t.Fatalf("create error playground: %v", err)
	}
}

func assertSupportedResourceListFilters(t *testing.T, srv *httptest.Server) {
	t.Helper()
	assertListNames(t, srv, "/api/marquees?status=active", []string{store.ConfiguredMarqueeName})
	assertListNames(t, srv, "/api/marquees?status=error", nil)
	assertListNames(t, srv, "/api/marquees?q=default", []string{store.ConfiguredMarqueeName})
	assertListNames(t, srv, "/api/marquees?name=default&sort=name_asc", []string{store.ConfiguredMarqueeName})
	assertListNames(t, srv, "/api/marquees?sort=name_desc", []string{store.ConfiguredMarqueeName})
	assertListNames(t, srv, "/api/marquees?page=2&per_page=1", nil)
	assertListNames(t, srv, "/api/marquees?created_before=2000-01-01", nil)
	assertListNames(t, srv, "/api/props?private=true", []string{"prop-filter-private"})
	assertListNames(t, srv, "/api/props?provider=github&status=active", []string{"prop-filter-public"})
	assertListNames(t, srv, "/api/props?name=prop-filter&sort=name_asc", []string{"prop-filter-private", "prop-filter-public"})
	assertListNames(t, srv, "/api/props?sort=name_asc", []string{"prop-filter-private", "prop-filter-public"})
	assertListNames(t, srv, "/api/playspecs?name=playspec-filter&sort=name_asc", []string{"playspec-filter-locked", "playspec-filter-plain"})
	assertListNames(t, srv, "/api/playspecs?locked=true", []string{"playspec-filter-locked"})
	assertListNames(t, srv, "/api/playgrounds?name=PLAYGROUND-FILTER&sort=name_asc", []string{"playground-filter-error", "playground-filter-running"})
	assertListNames(t, srv, "/api/playgrounds?name=playground-filter&sort=status_asc", []string{"playground-filter-error", "playground-filter-running"})
}

func assertInvalidResourceListFilters(t *testing.T, srv *httptest.Server) {
	t.Helper()
	for _, path := range []string{
		"/api/marquees?sort=this_is_not_a_real_column_xyz",
		"/api/marquees?sort=",
		"/api/marquees?sort=updated_at_desc",
		"/api/playgrounds?sort=updated_at_desc",
		"/api/marquees?status=",
		"/api/marquees?created_after=",
		"/api/marquees?created_after=not-a-time",
		"/api/marquees?created_before=",
		"/api/marquees?page=",
		"/api/marquees?page=abc",
		"/api/marquees?page=0",
		"/api/marquees?per_page=",
		"/api/marquees?per_page=abc",
		"/api/marquees?per_page=0",
		"/api/props?provider=",
		"/api/props?status=",
		"/api/props?private=",
		"/api/props?private=maybe",
		"/api/playspecs?locked=",
		"/api/playspecs?locked=maybe",
		"/api/playgrounds?status=",
	} {
		res := doReq(t, srv, http.MethodGet, path, nil, "test-token")
		assertBadRequest(t, res, path)
	}
}

func assertUnsupportedResourceListFilters(t *testing.T, srv *httptest.Server) {
	t.Helper()
	for _, path := range []string{
		"/api/playspecs?job_mode=true",
		"/api/playgrounds?job_mode=true",
		"/api/playgrounds?result_status=succeeded",
		"/api/playgrounds?result_status=success",
		"/api/props?provider=gitea",
	} {
		res := doReq(t, srv, http.MethodGet, path, nil, "test-token")
		assertErrorCode(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	}
}

func createRunningLogsPlayground(ctx context.Context, t *testing.T, st *store.DB, playgroundName string, project string) domain.Playground {
	t.Helper()
	mq := ensureTestConfiguredMarquee(t, st)
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           playgroundName,
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		Services:       []domain.PlaygroundServiceInfo{{Name: "web", Status: "running", Running: true}},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return pg
}

func newCurrentRowLogsFixture(t *testing.T) currentRowLogsFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_LOGS logs-current-new--1": {Stdout: "current-line\n"},
		},
	}
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: fake}})
	oldProject := "logs-current-old--1"
	newProject := "logs-current-new--1"
	pg := createRunningLogsPlayground(ctx, t, st, "logs-current-pg", oldProject)
	current := pg
	current.ComposeProject = &newProject
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save current project: %v", err)
	}
	return currentRowLogsFixture{ctx: ctx, fake: fake, app: app, playground: pg}
}

func assertCurrentRowLogsPayload(t *testing.T, payload map[string]any, fake *runtimetest.FakeExecutor) {
	t.Helper()
	if lines := payload["lines"].([]string); len(lines) != 1 || lines[0] != "current-line" {
		t.Fatalf("expected current live log line, got %#v", payload)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "base='/opt/fibe/playgrounds/logs-current-new--1'") || !strings.Contains(seen, "project='logs-current-new--1'") || !strings.Contains(seen, "FIBE_DISTILLED_LOGS") {
		t.Fatalf("logs should use current compose project, saw:\n%s", seen)
	}
	if strings.Contains(seen, "logs-current-old--1") {
		t.Fatalf("logs should not use stale compose project, saw:\n%s", seen)
	}
}

func newAllLiveLogsServer(t *testing.T) (*httptest.Server, *runtimetest.FakeExecutor) {
	t.Helper()
	ctx := context.Background()
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_LOGS all-live-logs--1 /opt/fibe/playgrounds/all-live-logs--1 web 2":    {Stdout: "web-one\nweb-two\n"},
			"FIBE_DISTILLED_LOGS all-live-logs--1 /opt/fibe/playgrounds/all-live-logs--1 worker 2": {Stdout: "worker-one\nworker-two\n"},
		},
	}
	srv, st := newTestServerWithStore(t, fake)
	t.Cleanup(srv.Close)

	mq := ensureTestConfiguredMarquee(t, st)
	project := "all-live-logs--1"
	if _, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "all-live-logs",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		Services: []domain.PlaygroundServiceInfo{
			{Name: "web", Status: "running", Running: true},
			{Name: "worker", Status: "running", Running: true},
		},
	}); err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return srv, fake
}

func requestPlaygroundLogs(t *testing.T, srv *httptest.Server, path string, body any) map[string]any {
	t.Helper()
	var queued map[string]any
	res := doReq(t, srv, http.MethodPost, path, body, "test-token")
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.StatusCode)
	}
	decodeResp(t, res, &queued)
	return waitAsyncSuccess(t, srv, queued["status_url"].(string))
}

func assertAggregateLiveLogs(t *testing.T, final map[string]any) {
	t.Helper()
	if final["source"] != "docker" || final["service"] != "" {
		t.Fatalf("expected aggregate docker logs, got %#v", final)
	}
	lines := final["lines"].([]any)
	if len(lines) != 4 || lines[0] != "[web] web-one" || lines[3] != "[worker] worker-two" {
		t.Fatalf("expected aggregate live lines, got %#v", final)
	}
	entries := final["entries"].([]any)
	if len(entries) != 4 || entries[2].(map[string]any)["service"] != "worker" || entries[2].(map[string]any)["line"] != "worker-one" {
		t.Fatalf("expected structured live entries, got %#v", final)
	}
}

func assertSeenCommands(t *testing.T, fake *runtimetest.FakeExecutor, commands []string) {
	t.Helper()
	seen := strings.Join(fake.Seen, "\n")
	for _, command := range commands {
		if !strings.Contains(seen, command) {
			t.Fatalf("expected command %q, saw:\n%s", command, seen)
		}
	}
}

func newRolloutClaimFixture(t *testing.T) rolloutClaimFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mq := ensureTestConfiguredMarquee(t, st)
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","Image":"alpine","State":"running","Health":"healthy","ExitCode":0}]`},
		},
	}
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: fake}})
	oldPS, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "rollout-old-spec",
		BaseComposeYAML: "services:\n  web:\n    image: nginx:old\n",
	})
	if err != nil {
		t.Fatalf("create old playspec: %v", err)
	}
	newPS, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "rollout-new-spec",
		BaseComposeYAML: "services:\n  web:\n    image: nginx:new\n",
	})
	if err != nil {
		t.Fatalf("create new playspec: %v", err)
	}
	project := "rollout-claim--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "rollout-claim-pg",
		Status:         domain.StatusRunning,
		PlayspecID:     oldPS.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	current := pg
	current.PlayspecID = newPS.ID
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save current playspec: %v", err)
	}
	return rolloutClaimFixture{ctx: ctx, store: st, app: app, playground: pg, newPlayspecID: *newPS.ID}
}

func assertRolloutRenderedCurrentPlayspec(t *testing.T, fixture rolloutClaimFixture) {
	t.Helper()
	persisted, err := fixture.store.GetPlayground(fixture.ctx, "rollout-claim-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.PlayspecID == nil || *persisted.PlayspecID != fixture.newPlayspecID {
		t.Fatalf("rollout should preserve current playspec id, got %#v", persisted.PlayspecID)
	}
	if !strings.Contains(persisted.GeneratedComposeYAML, "image: nginx:new") {
		t.Fatalf("rollout should render current playspec, got:\n%s", persisted.GeneratedComposeYAML)
	}
	if strings.Contains(persisted.GeneratedComposeYAML, "image: nginx:old") {
		t.Fatalf("rollout should not render stale playspec, got:\n%s", persisted.GeneratedComposeYAML)
	}
}

func newHardRestartReloadFixture(t *testing.T) hardRestartReloadFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	oldPS, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "restart-old-spec",
		BaseComposeYAML: "services:\n  web:\n    image: nginx:old\n",
	})
	if err != nil {
		t.Fatalf("create old playspec: %v", err)
	}
	newPS, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "restart-new-spec",
		BaseComposeYAML: "services:\n  web:\n    image: nginx:new\n",
	})
	if err != nil {
		t.Fatalf("create new playspec: %v", err)
	}
	mq := ensureTestConfiguredMarquee(t, st)
	project := "restart-reload--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "restart-reload-pg",
		Status:         domain.StatusHasChanges,
		PlayspecID:     oldPS.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec := &mutateDuringOperationExecutor{
		store:    st,
		mutateOn: "down --remove-orphans",
		mutate:   playspecDependencyMutation(*newPS.ID),
	}
	exec.playgroundID = pg.ID
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: exec}})
	return hardRestartReloadFixture{ctx: ctx, store: st, app: app, playground: pg, newPlayspec: newPS}
}

func playspecDependencyMutation(newPlayspecID int64) func(context.Context, *store.DB, int64) error {
	return func(ctx context.Context, st *store.DB, id int64) error {
		pg, err := st.GetPlayground(ctx, idString(id))
		if err != nil {
			return err
		}
		pg.PlayspecID = &newPlayspecID
		_, err = st.SavePlayground(ctx, pg)
		return err
	}
}

func assertHardRestartRenderedNewPlayspec(t *testing.T, persisted domain.Playground, newPlayspecID int64) {
	t.Helper()
	if persisted.PlayspecID == nil || *persisted.PlayspecID != newPlayspecID {
		t.Fatalf("hard restart should preserve current playspec id, got %#v", persisted.PlayspecID)
	}
	if !strings.Contains(persisted.GeneratedComposeYAML, "image: nginx:new") {
		t.Fatalf("hard restart should render current playspec, got:\n%s", persisted.GeneratedComposeYAML)
	}
	if strings.Contains(persisted.GeneratedComposeYAML, "image: nginx:old") {
		t.Fatalf("hard restart should not render stale playspec, got:\n%s", persisted.GeneratedComposeYAML)
	}
}

func commandErrorWithOutput(result runtime.CommandResult, err error) error {
	if err == nil {
		return nil
	}
	if output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr); output != "" {
		return errors.New(output)
	}
	return err
}

func newSupersededOperationApp(ctx context.Context, t *testing.T, fixture runtimePlaygroundFixture) (*Server, *runtimetest.FakeExecutor, domain.Playground) {
	t.Helper()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := &runtimetest.FakeExecutor{}
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: fake}})
	pg := createRuntimePlayground(ctx, t, st, fixture)
	current := pg
	current.Status = fixture.supersedingStatus
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save superseding status: %v", err)
	}
	return app, fake, pg
}

func createRuntimePlayground(ctx context.Context, t *testing.T, st *store.DB, fixture runtimePlaygroundFixture) domain.Playground {
	t.Helper()
	mq := ensureTestConfiguredMarquee(t, st)
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           fixture.playgroundName,
		Status:         fixture.initialStatus,
		MarqueeID:      &mq.ID,
		ComposeProject: &fixture.project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return pg
}

func playgroundStatusMutation(status string) func(context.Context, *store.DB, int64) error {
	return func(ctx context.Context, st *store.DB, id int64) error {
		pg, err := st.GetPlayground(ctx, idString(id))
		if err != nil {
			return err
		}
		pg.Status = status
		_, err = st.SavePlayground(ctx, pg)
		return err
	}
}

func assertSupersededOperationRejected(t *testing.T, rec *httptest.ResponseRecorder, fake *runtimetest.FakeExecutor, ok bool, operation string) {
	t.Helper()
	if ok {
		t.Fatalf("%s should fail before remote work", operation)
	}
	if rec.Code != http.StatusConflict && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected invalid-state rejection, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("%s should not run remote commands, saw:\n%s", operation, strings.Join(fake.Seen, "\n"))
	}
}

func failingStopExecutor() *runtimetest.FakeExecutor {
	return &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"FIBE_DISTILLED_STOP": errors.New("compose stop failed"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_STOP": {Stderr: "service refused to stop"},
		},
	}
}

func createStopFailurePlayground(t *testing.T, st *store.DB) {
	t.Helper()
	mq := ensureTestConfiguredMarquee(t, st)
	project := "stop-fail--1"
	_, err := st.CreatePlayground(context.Background(), domain.Playground{
		Name:           "stop-fail-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
}

func assertStopFailurePersisted(t *testing.T, st *store.DB) {
	t.Helper()
	pg, err := st.GetPlayground(context.Background(), "stop-fail-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if pg.Status != domain.StatusError || pg.StateReason == nil || *pg.StateReason != "compose_stop_failed" {
		t.Fatalf("stop failure should persist error state, got %#v", pg)
	}
	if !strings.Contains(*pg.ErrorMessage, "service refused to stop") {
		t.Fatalf("expected stop stderr in error message, got %#v", pg.ErrorMessage)
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _ := newTestServerWithStore(t, nil)
	return srv
}

func newTestServerWithGitHubBaseURL(t *testing.T, githubBaseURL string) *httptest.Server {
	t.Helper()
	srv, _ := newTestServerWithStoreAndGitHubBaseURL(t, nil, githubBaseURL)
	return srv
}

func newTestServerWithStore(t *testing.T, fake *runtimetest.FakeExecutor) (*httptest.Server, *store.DB) {
	t.Helper()
	return newTestServerWithStoreAndGitHubBaseURL(t, fake, "")
}

func newTestServerWithStoreAndGitHubBaseURL(t *testing.T, fake *runtimetest.FakeExecutor, githubBaseURL string) (*httptest.Server, *store.DB) {
	t.Helper()
	return newTestServerWithDBPathAndGitHubBaseURL(t, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"), fake, githubBaseURL)
}

func newTestServerWithDBPath(t *testing.T, dbPath string, fake *runtimetest.FakeExecutor) (*httptest.Server, *store.DB) {
	t.Helper()
	return newTestServerWithDBPathAndGitHubBaseURL(t, dbPath, fake, "")
}

func newTestServerWithDBPathAndGitHubBaseURL(t *testing.T, dbPath string, fake *runtimetest.FakeExecutor, githubBaseURL string) (*httptest.Server, *store.DB) {
	t.Helper()
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ensureTestConfiguredMarquee(t, st)
	cfg := config.Config{APIToken: "test-token", GitHubTok: "github-token"}
	if fake == nil {
		fake = &runtimetest.FakeExecutor{}
	}
	ensureDefaultComposePSResult(fake)
	wk := worker.Worker{
		DB: st,
		Runtime: runtime.Checker{
			Executor: fake,
		},
	}
	app := NewWithOptions(cfg, st, wk, Options{GitHubBaseURL: githubBaseURL})
	return httptest.NewServer(app), st
}

func ensureTestConfiguredMarquee(t *testing.T, st *store.DB) domain.Marquee {
	t.Helper()
	return ensureTestConfiguredMarqueeWith(t, st, domain.Marquee{})
}

func ensureTestConfiguredMarqueeWith(t *testing.T, st *store.DB, m domain.Marquee) domain.Marquee {
	t.Helper()
	domainName := "configured.example.test"
	acme := "ops@example.test"
	if m.Host == "" {
		m.Host = "localhost"
	}
	if m.User == "" {
		m.User = "local"
	}
	if m.DomainsInput == nil {
		m.DomainsInput = &domainName
	}
	if m.AcmeEmail == nil {
		m.AcmeEmail = &acme
	}
	if m.Status == "" {
		m.Status = "active"
	}
	m.Name = store.ConfiguredMarqueeName
	mq, err := st.EnsureConfiguredMarquee(context.Background(), domain.Marquee{
		Name:                 m.Name,
		Host:                 m.Host,
		Port:                 m.Port,
		User:                 m.User,
		DomainsInput:         m.DomainsInput,
		HTTPSEnabled:         m.HTTPSEnabled,
		TLSCertificateSource: m.TLSCertificateSource,
		AcmeEmail:            m.AcmeEmail,
		BuildPlatform:        m.BuildPlatform,
		SSHPrivateKey:        m.SSHPrivateKey,
		Status:               m.Status,
	})
	if err != nil {
		t.Fatalf("ensure configured marquee: %v", err)
	}
	return mq
}

func ensureDefaultComposePSResult(fake *runtimetest.FakeExecutor) {
	if fake.ResultContains == nil {
		fake.ResultContains = map[string]runtime.CommandResult{}
	}
	for fragment := range fake.ResultContains {
		if strings.Contains(fragment, "ps --all --format json") {
			return
		}
	}
	fake.ResultContains["ps --all --format json"] = runtime.CommandResult{Stdout: `[{"Service":"web","Image":"alpine","State":"running","Health":"healthy","ExitCode":0}]`}
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func doReq(t *testing.T, srv *httptest.Server, method string, path string, body any, token string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return res
}

func doRawPost(t *testing.T, srv *httptest.Server, path string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer test-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return res
}

func decodeResp(t *testing.T, res *http.Response, dst any) {
	t.Helper()
	defer closeResponseBody(t, res)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var body map[string]any
		_ = json.NewDecoder(res.Body).Decode(&body)
		t.Fatalf("unexpected status %d: %#v", res.StatusCode, body)
	}
	if err := json.NewDecoder(res.Body).Decode(dst); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func closeResponseBody(t *testing.T, res *http.Response) {
	t.Helper()
	if err := res.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
}

func closeSQLDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}
}

func assertStatus(t *testing.T, res *http.Response, status int, context string) {
	t.Helper()
	if res.StatusCode == status {
		return
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	closeResponseBody(t, res)
	t.Fatalf("%s, got %d: %#v", context, res.StatusCode, body)
}

func assertErrorCode(t *testing.T, res *http.Response, status int, code string) {
	t.Helper()
	defer closeResponseBody(t, res)
	if res.StatusCode != status {
		t.Fatalf("expected status %d, got %d", status, res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"].(map[string]any)["code"] != code {
		t.Fatalf("expected error code %s, got %#v", code, body)
	}
}

func assertErrorMessageContains(t *testing.T, res *http.Response, status int, code string, message string) {
	t.Helper()
	defer closeResponseBody(t, res)
	if res.StatusCode != status {
		t.Fatalf("expected status %d, got %d", status, res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	errBody := body["error"].(map[string]any)
	if errBody["code"] != code || !strings.Contains(errBody["message"].(string), message) {
		t.Fatalf("expected %s error containing %q, got %#v", code, message, body)
	}
}

func assertBadRequest(t *testing.T, res *http.Response, context string) {
	t.Helper()
	defer closeResponseBody(t, res)
	if res.StatusCode == http.StatusBadRequest {
		return
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	t.Fatalf("%s should return 400, got %d: %#v", context, res.StatusCode, body)
}

func assertBadRequestCases(t *testing.T, srv *httptest.Server, method string, cases []badRequestCase, context string) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res := doReq(t, srv, method, tt.path, tt.body, "test-token")
			assertBadRequest(t, res, context)
		})
	}
}

func assertBadRequestField(t *testing.T, res *http.Response, want string) {
	t.Helper()
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request for %s, got %d", want, res.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	fields := payload["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)
	if len(fields) != 1 || fields[0] != want {
		t.Fatalf("unexpected fields detail: %#v", payload)
	}
}

func playgroundServiceOverrideBody(name string, playspecID int64, services any) map[string]any {
	return map[string]any{"playground": map[string]any{
		"name":        name,
		"playspec_id": playspecID,
		"services":    services,
	}}
}

func assertPlaygroundActionDependencyError(t *testing.T, res *http.Response, dependency string) {
	t.Helper()
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusUnprocessableEntity {
		var body map[string]any
		_ = json.NewDecoder(res.Body).Decode(&body)
		t.Fatalf("expected missing %s to fail, got %d: %#v", dependency, res.StatusCode, body)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "PLAYGROUND_ACTION_FAILED" {
		t.Fatalf("unexpected error body: %#v", body)
	}
	details := errObj["details"].(map[string]any)
	if details["dependency"] != dependency {
		t.Fatalf("expected %s dependency detail, got %#v", dependency, body)
	}
}

func launchBodyWith(base map[string]any, extra map[string]any) map[string]any {
	body := map[string]any{}
	for key, value := range base {
		body[key] = value
	}
	for key, value := range extra {
		body[key] = value
	}
	return body
}

func assertListNames(t *testing.T, srv *httptest.Server, path string, want []string) {
	t.Helper()
	var list map[string]any
	res := doReq(t, srv, http.MethodGet, path, nil, "test-token")
	decodeResp(t, res, &list)
	data := list["data"].([]any)
	if len(data) != len(want) {
		t.Fatalf("%s expected %d rows, got %#v", path, len(want), list)
	}
	for i, raw := range data {
		got := raw.(map[string]any)["name"]
		if got != want[i] {
			t.Fatalf("%s row %d name=%v, want %s", path, i, got, want[i])
		}
	}
}

func startNonWritableGitHubRepoFixture(t *testing.T, fullName string) string {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+fullName {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default_branch": "main",
			"private":        true,
			"permissions":    map[string]any{"push": false, "admin": false, "maintain": false},
		})
	}))
	t.Cleanup(func() {
		gh.Close()
	})
	return gh.URL
}

func startPropSyncGitHubFixture(t *testing.T) string {
	t.Helper()
	var mu sync.Mutex
	var requestedPaths []string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestedPaths = append(requestedPaths, r.URL.Path)
		mu.Unlock()
		propSyncGitHubFixture(w, r)
	}))
	t.Cleanup(func() {
		gh.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, path := range requestedPaths {
			if strings.Contains(path, "/contents") {
				t.Fatalf("Prop sync must not fetch repository contents or env-default files, requested %s", path)
			}
		}
	})
	return gh.URL
}

func propSyncGitHubFixture(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/repos/acme/demo":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default_branch": "trunk",
			"private":        true,
			"permissions":    map[string]any{"push": true},
		})
	case "/repos/acme/demo/branches":
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "feature/z", "commit": map[string]any{"sha": "fffffff111111111111111111111111111111111"}},
			{"name": "trunk", "commit": map[string]any{"sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
			{"name": "bugfix/a", "commit": map[string]any{"sha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		})
	default:
		http.NotFound(w, r)
	}
}

func propBranches(t *testing.T, srv *httptest.Server, propName string, query string) []any {
	t.Helper()
	path := "/api/props/" + propName + "/branches"
	if query != "" {
		path += "?" + query
	}
	var branches map[string]any
	res := doReq(t, srv, http.MethodGet, path, nil, "test-token")
	decodeResp(t, res, &branches)
	return branches["branches"].([]any)
}

func assertBadPropBranchLimits(t *testing.T, srv *httptest.Server, propName string) {
	t.Helper()
	for _, query := range []string{"limit=abc", "limit=", "limit=0"} {
		res := doReq(t, srv, http.MethodGet, "/api/props/"+propName+"/branches?"+query, nil, "test-token")
		assertBadRequest(t, res, "bad branch "+query)
	}
}

func assertExpirationSummary(t *testing.T, pg map[string]any) {
	t.Helper()
	if pg["time_remaining"].(float64) <= 9*60*60 {
		t.Fatalf("expected detailed playground time_remaining, got %#v", pg)
	}
	if pg["expiration_percentage"] == nil {
		t.Fatalf("expected expiration percentage in playground response: %#v", pg)
	}
}

func assertExpirationExtension(t *testing.T, ext expirationResponse, future time.Time) {
	t.Helper()
	if ext.ID == 0 || ext.TimeRemaining <= 11*60*60 {
		t.Fatalf("unexpected extension result: %#v", ext)
	}
	if ext.ExpiresAt.Before(future.Add(119*time.Minute)) || ext.ExpiresAt.After(future.Add(121*time.Minute)) {
		t.Fatalf("expiration should extend from current future expiry: before=%s after=%s", future, ext.ExpiresAt)
	}
}

func assertDefaultTTL(t *testing.T, pg map[string]any, start time.Time) {
	t.Helper()
	expiresRaw, ok := pg["expires_at"].(string)
	if !ok || expiresRaw == "" {
		t.Fatalf("never_expire=false should set default expires_at: %#v", pg)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil {
		t.Fatalf("parse expires_at %q: %v", expiresRaw, err)
	}
	if expiresAt.Before(start.Add(7*time.Hour+55*time.Minute)) || expiresAt.After(start.Add(8*time.Hour+5*time.Minute)) {
		t.Fatalf("default TTL should be about 8h, got %s from %s", expiresAt, start)
	}
}

func assertInvalidCreateExpirations(t *testing.T, srv *httptest.Server, playspecID int64) {
	t.Helper()
	const (
		minJSONUnixTimestamp = -62167219200
		maxJSONUnixTimestamp = 253402300799
	)
	for _, invalid := range []any{"not-a-timestamp", "", 123.5, nil, int64(minJSONUnixTimestamp - 1), int64(maxJSONUnixTimestamp + 1)} {
		res := doReq(t, srv, http.MethodPost, "/api/playgrounds", map[string]any{"playground": map[string]any{
			"name":        "invalid-expiry-pg",
			"playspec_id": playspecID,
			"expires_at":  invalid,
		}}, "test-token")
		assertBadRequest(t, res, fmt.Sprintf("invalid create expires_at %#v", invalid))
	}
}

func waitAsyncSuccess(t *testing.T, srv *httptest.Server, statusURL string) map[string]any {
	t.Helper()
	return waitAsyncStatus(t, srv, statusURL, "success", "error")
}

func waitAsyncError(t *testing.T, srv *httptest.Server, statusURL string) map[string]any {
	t.Helper()
	return waitAsyncStatus(t, srv, statusURL, "error", "success")
}

func waitAsyncStatus(t *testing.T, srv *httptest.Server, statusURL string, want string, fail string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var body map[string]any
	for time.Now().Before(deadline) {
		res := doReq(t, srv, http.MethodGet, statusURL, nil, "test-token")
		decodeResp(t, res, &body)
		switch body["status"] {
		case want:
			return body
		case fail:
			t.Fatalf("async reached %s before %s: %#v", fail, want, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("async did not complete with %s: %#v", want, body)
	return nil
}

func assertBadRequestWithoutSecret(t *testing.T, res *http.Response) {
	t.Helper()
	const secret = "ghp_secret"
	defer closeResponseBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		var body map[string]any
		_ = json.NewDecoder(res.Body).Decode(&body)
		t.Fatalf("expected 400, got %d: %#v", res.StatusCode, body)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if strings.Contains(fmt.Sprint(body), secret) {
		t.Fatalf("error response leaked secret %q: %#v", secret, body)
	}
}

func numberID(v any) string {
	return idString(int64(v.(float64)))
}

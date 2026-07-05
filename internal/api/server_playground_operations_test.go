package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"github.com/fibegg/fibe-distilled/internal/worker"
)

func TestPlaygroundDeleteFailsClosedWhenRemoteDestroyFails(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"down --remove-orphans -v": errors.New("exit status 1"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"down --remove-orphans -v": {Stderr: "docker compose down failed"},
		},
	}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "delete-spec",
		"base_compose_yaml": `services:
  web:
    image: alpine
`,
	}}
	var playspec map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	marquee := ensureTestConfiguredMarquee(t, st)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "delete-pg",
		"playspec_id": int64(playspec["id"].(float64)),
		"marquee_id":  marquee.ID,
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)
	waitForPlaygroundStatus(t, st, "delete-pg", domain.StatusRunning)

	res = doReq(t, srv, http.MethodDelete, "/api/playgrounds/delete-pg", nil, "test-token")
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected destroy failure status, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	closeResponseBody(t, res)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "PLAYGROUND_DESTROY_FAILED" {
		t.Fatalf("unexpected error body: %#v", body)
	}

	res = doReq(t, srv, http.MethodGet, "/api/playgrounds/delete-pg", nil, "test-token")
	decodeResp(t, res, &pg)
	if pg["status"] != "error" {
		t.Fatalf("failed destroy should preserve row in error status, got %#v", pg)
	}
	details := pg["error_details"].(map[string]any)
	if details["destroy_failure"] == nil {
		t.Fatalf("expected destroy failure details, got %#v", pg)
	}
}

func TestPlaygroundDeleteFailureDoesNotOverwriteSupersedingLifecycleStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exec := &mutateDuringOperationExecutor{
		mutateOn: "down --remove-orphans -v",
		err:      errors.New("compose destroy failed"),
		result:   runtime.CommandResult{Stderr: "destroy refused"},
		mutate:   playgroundStatusMutation(domain.StatusStopped),
	}
	exec.store = st
	srv := httptest.NewServer(New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}))
	defer srv.Close()

	mq := ensureTestConfiguredMarquee(t, st)
	project := "delete-superseded--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "delete-superseded-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec.playgroundID = pg.ID

	res := doReq(t, srv, http.MethodDelete, "/api/playgrounds/delete-superseded-pg", nil, "test-token")
	if res.StatusCode != http.StatusConflict {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected superseded delete conflict, got %d: %#v", res.StatusCode, got)
	}
	closeResponseBody(t, res)
	persisted, err := st.GetPlayground(ctx, "delete-superseded-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("delete failure must not overwrite superseding stopped status, got %#v", persisted)
	}
	if persisted.ErrorMessage != nil {
		t.Fatalf("delete failure must not write stale error details, got %#v", persisted.ErrorMessage)
	}
}

func TestPlaygroundOperationsRequirePlayspecForDeployActions(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	for _, tc := range []struct {
		name   string
		action string
		status string
	}{
		{name: "rollout", action: "rollout", status: domain.StatusRunning},
		{name: "spaced-rollout", action: " rollout ", status: domain.StatusRunning},
		{name: "retry", action: "retry_compose", status: domain.StatusRunning},
		{name: "start", action: "start", status: domain.StatusStopped},
		{name: "hard-restart", action: "hard_restart", status: domain.StatusRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg, err := st.CreatePlayground(context.Background(), domain.Playground{
				Name:   "no-playspec-" + tc.name,
				Status: tc.status,
			})
			if err != nil {
				t.Fatalf("create playground: %v", err)
			}
			body := map[string]any{"action_type": tc.action}
			res := doReq(t, srv, http.MethodPost, "/api/playgrounds/"+pg.Name+"/operations", body, "test-token")
			assertPlaygroundActionDependencyError(t, res, "playspec")
		})
	}
}

func TestPlaygroundOperationsValidateStateAndHonorForce(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()

	stopped, err := st.CreatePlayground(context.Background(), domain.Playground{
		Name:   "stopped-operation-pg",
		Status: domain.StatusStopped,
	})
	if err != nil {
		t.Fatalf("create stopped playground: %v", err)
	}

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/"+stopped.Name+"/operations", map[string]any{"action_type": "stop"}, "test-token")
	if res.StatusCode != http.StatusUnprocessableEntity {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected stopped stop without force to fail, got %d: %#v", res.StatusCode, got)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode invalid state: %v", err)
	}
	closeResponseBody(t, res)
	if got["error"].(map[string]any)["code"] != "INVALID_STATE" {
		t.Fatalf("expected invalid state error, got %#v", got)
	}

	res = doReq(t, srv, http.MethodPost, "/api/playgrounds/"+stopped.Name+"/operations", map[string]any{"action_type": "stop", "force": true}, "test-token")
	if res.StatusCode != http.StatusAccepted {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected forced stopped stop to be accepted, got %d: %#v", res.StatusCode, got)
	}
	closeResponseBody(t, res)

	pending, err := st.CreatePlayground(context.Background(), domain.Playground{
		Name:   "pending-force-rollout-pg",
		Status: domain.StatusPending,
	})
	if err != nil {
		t.Fatalf("create pending playground: %v", err)
	}
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds/"+pending.Name+"/operations", map[string]any{"action_type": "rollout", "force": true}, "test-token")
	if res.StatusCode != http.StatusConflict {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected forced rollout during active creation to conflict, got %d: %#v", res.StatusCode, got)
	}
	got = map[string]any{}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode active creation conflict: %v", err)
	}
	closeResponseBody(t, res)
	details := got["error"].(map[string]any)["details"].(map[string]any)
	if details["force_allowed"] != false {
		t.Fatalf("expected active creation to reject force, got %#v", got)
	}
}

func TestPlaygroundHardRestartWithoutPlayspecRestartsExistingRuntimeCompose(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	srv, st := newTestServerWithStore(t, fake)
	defer srv.Close()

	mq := ensureTestConfiguredMarquee(t, st)
	project := "runtime-only--1"
	_, err := st.CreatePlayground(context.Background(), domain.Playground{
		Name:           "runtime-only-pg",
		Status:         domain.StatusStopped,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/runtime-only-pg/operations", map[string]any{"action_type": "hard_restart", "force": true}, "test-token")
	if res.StatusCode != http.StatusAccepted {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected hard restart to be accepted, got %d: %#v", res.StatusCode, got)
	}
	var queued map[string]any
	if err := json.NewDecoder(res.Body).Decode(&queued); err != nil {
		t.Fatalf("decode async response: %v", err)
	}
	closeResponseBody(t, res)
	waitAsyncSuccess(t, srv, queued["status_url"].(string))

	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "FIBE_DISTILLED_DOWN project='runtime-only--1'") ||
		!strings.Contains(seen, "docker compose -f compose.yml -p 'runtime-only--1' down --remove-orphans") {
		t.Fatalf("expected hard restart to run compose down:\n%s", seen)
	}
	if !strings.Contains(seen, "FIBE_DISTILLED_START") || !strings.Contains(seen, "up -d --remove-orphans --pull missing") {
		t.Fatalf("expected hard restart to start existing compose after down:\n%s", seen)
	}

	var pg map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/playgrounds/runtime-only-pg", nil, "test-token")
	decodeResp(t, res, &pg)
	if pg["status"] != domain.StatusRunning {
		t.Fatalf("expected runtime-only playground to be running, got %#v", pg)
	}
}

func TestPlaygroundOperationUsesConfiguredMarqueeForLegacyRuntimeRow(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	srv, st := newTestServerWithDBPath(t, dbPath, fake)
	defer srv.Close()

	ctx := context.Background()
	legacy := runtimetest.InsertLegacyMarquee(ctx, t, dbPath, domain.Marquee{Name: "legacy-operation-marquee", Host: "127.0.0.1", User: "root", Port: 22, Status: "active"})
	configured := ensureTestConfiguredMarqueeWith(t, st, domain.Marquee{Host: "127.0.0.2"})
	project := "legacy-operation--1"
	_, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "legacy-operation-pg",
		Status:         domain.StatusStopped,
		MarqueeID:      &legacy.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/legacy-operation-pg/operations", map[string]any{"action_type": "start"}, "test-token")
	if res.StatusCode != http.StatusAccepted {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected start to be accepted, got %d: %#v", res.StatusCode, got)
	}
	var queued map[string]any
	if err := json.NewDecoder(res.Body).Decode(&queued); err != nil {
		t.Fatalf("decode async response: %v", err)
	}
	closeResponseBody(t, res)
	waitAsyncSuccess(t, srv, queued["status_url"].(string))
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "marquee_id="+idString(configured.ID)) {
		t.Fatalf("operation should use configured marquee owner guard:\n%s", seen)
	}
	if strings.Contains(seen, "marquee_id="+idString(legacy.ID)) {
		t.Fatalf("operation must not use stale legacy marquee id:\n%s", seen)
	}
}

func TestPlaygroundStartPreservesConcurrentExpirationUpdate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exec := &mutateDuringOperationExecutor{
		store:    st,
		mutateOn: "FIBE_DISTILLED_START",
		mutate: func(ctx context.Context, st *store.DB, id int64) error {
			pg, err := st.GetPlayground(ctx, idString(id))
			if err != nil {
				return err
			}
			future := time.Now().UTC().Add(2 * time.Hour)
			pg.ExpiresAt = &future
			_, err = st.SavePlayground(ctx, pg)
			return err
		},
	}
	srv := httptest.NewServer(New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}))
	defer srv.Close()

	mq := ensureTestConfiguredMarquee(t, st)
	project := "start-expiry--1"
	past := time.Now().UTC().Add(-time.Hour)
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "start-expiry-pg",
		Status:         domain.StatusStopped,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		ExpiresAt:      &past,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec.playgroundID = pg.ID

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/start-expiry-pg/operations", map[string]any{"action_type": "start"}, "test-token")
	if res.StatusCode != http.StatusAccepted {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected start to be accepted, got %d: %#v", res.StatusCode, got)
	}
	var queued map[string]any
	if err := json.NewDecoder(res.Body).Decode(&queued); err != nil {
		t.Fatalf("decode async response: %v", err)
	}
	closeResponseBody(t, res)
	waitAsyncSuccess(t, srv, queued["status_url"].(string))
	persisted, err := st.GetPlayground(ctx, "start-expiry-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusRunning {
		t.Fatalf("expected running status, got %#v", persisted)
	}
	if persisted.ExpiresAt == nil || !persisted.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("operation status save must preserve concurrent expiration update, got %#v", persisted.ExpiresAt)
	}
}

func TestPlaygroundStartDoesNotOverwriteSupersedingLifecycleStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exec := &mutateDuringOperationExecutor{
		store:    st,
		mutateOn: "FIBE_DISTILLED_START",
		mutate: func(ctx context.Context, st *store.DB, id int64) error {
			pg, err := st.GetPlayground(ctx, idString(id))
			if err != nil {
				return err
			}
			pg.Status = domain.StatusDestroying
			_, err = st.SavePlayground(ctx, pg)
			return err
		},
	}
	srv := httptest.NewServer(New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}))
	defer srv.Close()

	mq := ensureTestConfiguredMarquee(t, st)
	project := "start-superseded--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "start-superseded-pg",
		Status:         domain.StatusStopped,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec.playgroundID = pg.ID

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/start-superseded-pg/operations", map[string]any{"action_type": "start"}, "test-token")
	if res.StatusCode != http.StatusAccepted {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected superseded start to be accepted asynchronously, got %d: %#v", res.StatusCode, got)
	}
	var queued map[string]any
	if err := json.NewDecoder(res.Body).Decode(&queued); err != nil {
		t.Fatalf("decode async response: %v", err)
	}
	closeResponseBody(t, res)
	failed := waitAsyncError(t, srv, queued["status_url"].(string))
	if failed["error_code"] != "INVALID_STATE" {
		t.Fatalf("expected invalid state async error, got %#v", failed)
	}
	persisted, err := st.GetPlayground(ctx, "start-superseded-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusDestroying {
		t.Fatalf("operation must not overwrite superseding destroying status, got %#v", persisted)
	}
}

func TestPlaygroundStartClaimsCurrentRowBeforeRemoteCommand(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := &runtimetest.FakeExecutor{}
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: fake}})
	mq := ensureTestConfiguredMarquee(t, st)
	oldProject := "start-claim-old--1"
	newProject := "start-claim-new--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "start-claim-pg",
		Status:         domain.StatusStopped,
		MarqueeID:      &mq.ID,
		ComposeProject: &oldProject,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	current := pg
	current.ComposeProject = &newProject
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save current project: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playgrounds/start-claim-pg/operations", nil)
	if _, ok := app.applyPlaygroundOperation(rec, req, pg, "start"); !ok {
		t.Fatalf("start should use current row and succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "project='start-claim-new--1'") {
		t.Fatalf("start should use current compose project, saw:\n%s", seen)
	}
	if strings.Contains(seen, "project='start-claim-old--1'") {
		t.Fatalf("start should not use stale compose project, saw:\n%s", seen)
	}
}

func TestPlaygroundStartBranchesOnCurrentRuntimeCompose(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := &runtimetest.FakeExecutor{}
	app := New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: fake}})
	mq := ensureTestConfiguredMarquee(t, st)
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:   "start-current-runtime-pg",
		Status: domain.StatusStopped,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	project := "start-current-runtime--1"
	current := pg
	current.MarqueeID = &mq.ID
	current.ComposeProject = &project
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save current runtime identity: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playgrounds/start-current-runtime-pg/operations", nil)
	if _, ok := app.applyPlaygroundOperation(rec, req, pg, "start"); !ok {
		t.Fatalf("start should branch on current runtime compose and succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "FIBE_DISTILLED_START") || !strings.Contains(seen, "project='start-current-runtime--1'") {
		t.Fatalf("start should run current runtime compose project, saw:\n%s", seen)
	}
}

func TestPlaygroundRolloutClaimsCurrentRowBeforeDependencyLookup(t *testing.T) {
	fixture := newRolloutClaimFixture(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playgrounds/rollout-claim-pg/operations", nil)
	if _, ok := fixture.app.deployPlaygroundOperation(rec, req, fixture.playground); !ok {
		t.Fatalf("rollout should use current row and succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	assertRolloutRenderedCurrentPlayspec(t, fixture)
}

func TestPlaygroundSupersededOperationsSkipRemoteWork(t *testing.T) {
	type supersededOperationCase struct {
		name      string
		fixture   runtimePlaygroundFixture
		path      string
		operation func(*Server, *httptest.ResponseRecorder, *http.Request, domain.Playground) bool
	}
	newCase := func(name string, prefix string, initialStatus string, operation func(*Server, *httptest.ResponseRecorder, *http.Request, domain.Playground) bool) supersededOperationCase {
		return supersededOperationCase{
			name: name,
			fixture: runtimePlaygroundFixture{
				marqueeName:       prefix + "-skip-marquee",
				playgroundName:    prefix + "-skip-pg",
				project:           prefix + "-skip--1",
				initialStatus:     initialStatus,
				supersedingStatus: domain.StatusDestroying,
			},
			path:      "/api/playgrounds/" + prefix + "-skip-pg/operations",
			operation: operation,
		}
	}
	cases := []supersededOperationCase{
		newCase("start", "start", domain.StatusStopped, func(app *Server, rec *httptest.ResponseRecorder, req *http.Request, pg domain.Playground) bool {
			_, ok := app.applyPlaygroundOperation(rec, req, pg, "start")
			return ok
		}),
		newCase("hard_restart", "restart", domain.StatusRunning, func(app *Server, rec *httptest.ResponseRecorder, req *http.Request, pg domain.Playground) bool {
			_, ok := app.downExistingComposeForRestart(rec, req, pg)
			return ok
		}),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			app, fake, pg := newSupersededOperationApp(ctx, t, tc.fixture)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			ok := tc.operation(app, rec, req, pg)
			assertSupersededOperationRejected(t, rec, fake, ok, "stale "+tc.name)
		})
	}
}

func TestPlaygroundHardRestartReloadsDependenciesAfterDown(t *testing.T) {
	fixture := newHardRestartReloadFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playgrounds/restart-reload-pg/operations", nil)
	if _, ok := fixture.app.hardRestartPlayground(rec, req, fixture.playground); !ok {
		t.Fatalf("hard restart should reload current dependencies and succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	persisted, err := fixture.store.GetPlayground(fixture.ctx, "restart-reload-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	assertHardRestartRenderedNewPlayspec(t, persisted, *fixture.newPlayspec.ID)
}

func TestPlaygroundStopFailurePersistsErrorState(t *testing.T) {
	srv, st := newTestServerWithStore(t, failingStopExecutor())
	defer srv.Close()
	createStopFailurePlayground(t, st)

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/stop-fail-pg/operations", map[string]any{"action_type": "stop"}, "test-token")
	assertErrorMessageContains(t, res, http.StatusUnprocessableEntity, "PLAYGROUND_ACTION_FAILED", "service refused to stop")
	assertStopFailurePersisted(t, st)
}

func TestPlaygroundStopFailureDoesNotOverwriteSupersedingLifecycleStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exec := &mutateDuringOperationExecutor{
		mutateOn: "FIBE_DISTILLED_STOP",
		err:      errors.New("compose stop failed"),
		result:   runtime.CommandResult{Stderr: "service refused to stop"},
		mutate:   playgroundStatusMutation(domain.StatusDestroying),
	}
	exec.store = st
	srv := httptest.NewServer(New(config.Config{APIToken: "test-token"}, st, worker.Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}))
	defer srv.Close()

	mq := ensureTestConfiguredMarquee(t, st)
	project := "stop-superseded--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "stop-superseded-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec.playgroundID = pg.ID

	res := doReq(t, srv, http.MethodPost, "/api/playgrounds/stop-superseded-pg/operations", map[string]any{"action_type": "stop"}, "test-token")
	if res.StatusCode != http.StatusConflict {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected superseded stop conflict, got %d: %#v", res.StatusCode, got)
	}
	closeResponseBody(t, res)
	persisted, err := st.GetPlayground(ctx, "stop-superseded-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusDestroying {
		t.Fatalf("stop failure must not overwrite superseding destroying status, got %#v", persisted)
	}
	if persisted.ErrorMessage != nil {
		t.Fatalf("stop failure must not write stale error details, got %#v", persisted.ErrorMessage)
	}
}

package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

func assertSourceSyncCommandExcludesUnsafeFallbacks(t *testing.T, command string) {
	t.Helper()
	for _, forbidden := range []string{"/props/test/feature/new-ui", "pull --ff-only || true", "printf '0 0'"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("source sync command contains unsafe fallback %q:\n%s", forbidden, command)
		}
	}
}

func assertSourceSyncCommandIncludesTypedRequest(t *testing.T, command string, branch string) {
	t.Helper()
	for _, want := range []string{
		"FIBE_DISTILLED_SOURCE_SYNC",
		"https://github.com/acme/my-api.git",
		branch,
		"github_auth=true",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("expected source-sync request field %q in command:\n%s", want, command)
		}
	}
}

func assertSourceSyncRemoteCommandsNotRun(t *testing.T, fake *runtimetest.FakeExecutor, reason string) {
	t.Helper()
	if len(fake.Seen) != 0 {
		t.Fatalf("%s must not run source-sync commands:\n%s", reason, strings.Join(fake.Seen, "\n"))
	}
}

func mustRefreshPlayground(t *testing.T, w Worker, ctx context.Context, pg domain.Playground) domain.PlaygroundStatus {
	t.Helper()
	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	return status
}

func runSourceSyncScriptWithRevList(t *testing.T, revListCmd string) (string, int) {
	t.Helper()
	return runSourceSyncScript(t, "exit 0", revListCmd)
}

func runSourceSyncScript(t *testing.T, statusCmd string, revListCmd string) (string, int) {
	t.Helper()
	gitScript := `#!/bin/sh
case "$*" in
  *"status --porcelain"*) ` + statusCmd + ` ;;
  *"fetch --all --prune"*) exit 0 ;;
  *"rev-parse --verify origin/main"*) exit 0 ;;
  *"checkout main"*) exit 0 ;;
  *"rev-list --left-right --count"*) ` + revListCmd + ` ;;
  *"pull --ff-only"*) echo "pull ok"; exit 0 ;;
esac
exit 0
`
	return runSourceSyncScriptWithGitScript(t, gitScript)
}

func runSourceSyncScriptWithGitScript(t *testing.T, gitScript string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(target, ".git"), 0o755); err != nil {
		t.Fatalf("create fake checkout: %v", err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	gitPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(gitPath, []byte(gitScript), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	cmd := exec.Command("sh", "-eu")
	cmd.Stdin = strings.NewReader(sourceSyncScript)
	cmd.Env = []string{
		"PATH=" + binDir + ":/usr/bin:/bin",
		"parent=" + dir,
		"target=" + target,
		"repo=https://github.com/acme/demo.git",
		"auth_header=",
		"branch=main",
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("unexpected source sync error: %v output:\n%s", err, output)
	}
	return string(output), exitErr.ExitCode()
}

func redactedCredentialSourceSyncError(t *testing.T) sourceSyncError {
	t.Helper()
	raw := "fatal: Authentication failed for 'https://x-access-token:ghp_secret@github.com/acme/private.git'\n" +
		"fatal: Authentication failed for 'http://user:password@example.com/acme/private.git'\n" +
		"fatal: Authentication failed for 'https://ghp_useronly@github.com/acme/useronly.git'\n" +
		"git -c http.extraHeader=Authorization: Basic eC1hY2Nlc3MtdG9rZW46Z2hwX3NlY3JldA== fetch"
	err := classifySourceSyncError("web", "https://x-access-token:ghp_secret@github.com/acme/private.git", "main", "/opt/fibe/source", runtime.CommandResult{Stderr: raw}, errors.New("git failed"))
	var syncErr sourceSyncError
	if !errors.As(err, &syncErr) {
		t.Fatalf("expected sourceSyncError, got %T %[1]v", err)
	}
	return syncErr
}

func assertSourceSyncDiagnosticsRedacted(t *testing.T, syncErr sourceSyncError) {
	t.Helper()
	details := syncErr.Details()
	for _, value := range []string{syncErr.Message, syncErr.Output, details["output"].(string), details["message"].(string), details["repository_url"].(string)} {
		if strings.Contains(value, "ghp_secret") || strings.Contains(value, "ghp_useronly") || strings.Contains(value, "eC1hY2Nlc3MtdG9rZW46Z2hwX3NlY3JldA==") {
			t.Fatalf("source sync diagnostics leaked credential: %q", value)
		}
	}
	if details["repository_url"] != "https://***@github.com/acme/private.git" {
		t.Fatalf("expected redacted repository_url detail, got %#v", details)
	}
}

func assertSourceSyncOutputRedacted(t *testing.T, output string) {
	t.Helper()
	if !strings.Contains(output, "https://***@github.com/acme/private.git") {
		t.Fatalf("expected redacted credentialed URL, got %q", output)
	}
	if !strings.Contains(output, "http://***@example.com/acme/private.git") {
		t.Fatalf("expected redacted HTTP credentialed URL, got %q", output)
	}
	if !strings.Contains(output, "https://***@github.com/acme/useronly.git") {
		t.Fatalf("expected redacted userinfo-only credentialed URL, got %q", output)
	}
	if !strings.Contains(output, "Authorization: Basic ***") {
		t.Fatalf("expected redacted authorization header, got %q", output)
	}
}

func TestEnqueueRunsOperationAsynchronously(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	started := make(chan struct{})
	release := make(chan struct{})
	w := Worker{DB: st}
	op, err := w.Enqueue(ctx, func(context.Context) (map[string]any, *domain.APIError) {
		close(started)
		<-release
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if op.Status != domain.AsyncQueued {
		t.Fatalf("enqueue should return queued operation, got %#v", op)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("async operation did not start")
	}
	pending, err := st.GetAsync(ctx, op.ID)
	if err != nil {
		t.Fatalf("get async: %v", err)
	}
	if pending.Status == domain.AsyncSuccess {
		t.Fatalf("operation completed before release: %#v", pending)
	}
	close(release)
	final := waitAsyncOperationStatus(t, ctx, st, op.ID, domain.AsyncSuccess)
	if final.Status != domain.AsyncSuccess || final.Payload["ok"] != true {
		t.Fatalf("expected async success payload, got %#v", final)
	}
}

func waitAsyncOperationStatus(t *testing.T, ctx context.Context, st *store.DB, id string, want string) domain.AsyncOperation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op, err := st.GetAsync(ctx, id)
		if err != nil {
			t.Fatalf("get async operation %s: %v", id, err)
		}
		if op.Status == want {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	final, err := st.GetAsync(ctx, id)
	if err != nil {
		t.Fatalf("get final async operation %s: %v", id, err)
	}
	t.Fatalf("async operation %s did not reach %s: %#v", id, want, final)
	return domain.AsyncOperation{}
}

func TestRunAsyncTerminalizesUnencodableSuccessPayload(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	w := Worker{DB: st}
	op, err := st.CreateAsync(ctx, domain.AsyncOperation{ID: "bad-payload", Status: domain.AsyncQueued})
	if err != nil {
		t.Fatalf("create async: %v", err)
	}

	w.runAsync(ctx, op, func(context.Context) (map[string]any, *domain.APIError) {
		return map[string]any{"bad": math.Inf(1)}, nil
	})

	final, err := st.GetAsync(ctx, op.ID)
	if err != nil {
		t.Fatalf("get async: %v", err)
	}
	if final.Status != domain.AsyncError || final.Error == nil || final.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("expected terminal internal error, got %#v", final)
	}
	if !strings.Contains(final.Error.Message, "could not be persisted") {
		t.Fatalf("expected persistence error message, got %#v", final.Error)
	}
	if cause, _ := final.Error.Details["cause"].(string); !strings.Contains(cause, "async_operations.payload_json") {
		t.Fatalf("expected payload JSON cause, got %#v", final.Error.Details)
	}
}

func TestRunAsyncDoesNotExposePanicValue(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	w := Worker{DB: st}
	op, err := st.CreateAsync(ctx, domain.AsyncOperation{ID: "panic-payload", Status: domain.AsyncQueued})
	if err != nil {
		t.Fatalf("create async: %v", err)
	}

	w.runAsync(ctx, op, func(context.Context) (map[string]any, *domain.APIError) {
		panic("secret-token-value")
	})

	final, err := st.GetAsync(ctx, op.ID)
	if err != nil {
		t.Fatalf("get async: %v", err)
	}
	if final.Status != domain.AsyncError || final.Error == nil || final.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("expected terminal internal error, got %#v", final)
	}
	if final.Error.Message != "async operation failed unexpectedly" {
		t.Fatalf("expected generic panic error, got %#v", final.Error)
	}
	if strings.Contains(fmt.Sprint(final.Error), "secret-token-value") || strings.Contains(fmt.Sprint(final.Error), "panic") {
		t.Fatalf("panic value leaked to async error: %#v", final.Error)
	}
}

type stopDuringDeployExecutor struct {
	runtimetest.FakeExecutor
	store        *store.DB
	playgroundID int64
	mutateOn     string
	mutated      bool
}

func (e *stopDuringDeployExecutor) Run(ctx context.Context, _ domain.Marquee, command string) (runtime.CommandResult, error) {
	e.Seen = append(e.Seen, command)
	if !e.mutated && (e.mutateOn == "" || strings.Contains(command, e.mutateOn)) {
		e.mutated = true
		if err := markPlaygroundStopped(ctx, e.store, e.playgroundID); err != nil {
			return runtime.CommandResult{}, err
		}
	}
	if strings.Contains(command, "ps --all --format json") {
		return runtime.CommandResult{Stdout: `[{"Service":"web","Image":"nginx:alpine","State":"running","Health":"healthy","ExitCode":0}]`}, nil
	}
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *stopDuringDeployExecutor) Ping(ctx context.Context, marquee domain.Marquee) error {
	_, err := e.Run(ctx, marquee, "docker:ping")
	return err
}

func (e *stopDuringDeployExecutor) Up(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	_, err := e.Run(ctx, marquee, runtimetest.ComposeUpCommand("", project, base, marquee.ID))
	return err
}

func (e *stopDuringDeployExecutor) Stop(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	_, err := e.Run(ctx, marquee, runtimetest.ComposeStopCommand("", project, base, marquee.ID))
	return err
}

func (e *stopDuringDeployExecutor) Services(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) ([]domain.PlaygroundServiceInfo, error) {
	return inspectRunningWebService(ctx, e, marquee, project, base)
}

type workerTestRunner interface {
	Run(context.Context, domain.Marquee, string) (runtime.CommandResult, error)
}

func inspectRunningWebService(ctx context.Context, runner workerTestRunner, marquee domain.Marquee, project string, base string) ([]domain.PlaygroundServiceInfo, error) {
	_, err := runner.Run(ctx, marquee, runtimetest.ComposeInspectCommand(project, base, marquee.ID))
	if err != nil {
		return nil, err
	}
	return []domain.PlaygroundServiceInfo{{Name: "web", Image: "nginx:alpine", Status: "running", Health: "healthy", Running: true}}, nil
}

func markPlaygroundStopped(ctx context.Context, st *store.DB, id int64) error {
	pg, err := st.GetPlayground(ctx, strconv.FormatInt(id, 10))
	if err != nil {
		return err
	}
	pg.Status = domain.StatusStopped
	_, err = st.SavePlayground(ctx, pg)
	return err
}

func extendPlaygroundExpiration(ctx context.Context, st *store.DB, id int64) error {
	pg, err := st.GetPlayground(ctx, strconv.FormatInt(id, 10))
	if err != nil {
		return err
	}
	future := time.Now().UTC().Add(time.Hour)
	pg.ExpiresAt = &future
	_, err = st.SavePlayground(ctx, pg)
	return err
}

type deploymentExpirationFixture struct {
	ctx            context.Context
	store          *store.DB
	playground     domain.Playground
	extendedExpiry time.Time
}

func newDeploymentExpirationFixture(t *testing.T, name string) deploymentExpirationFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	originalExpiry := time.Now().UTC().Add(time.Hour)
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:      name,
		Status:    domain.StatusPending,
		ExpiresAt: &originalExpiry,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	extendedExpiry := originalExpiry.Add(6 * time.Hour)
	current := pg
	current.ExpiresAt = &extendedExpiry
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save current expiration: %v", err)
	}
	return deploymentExpirationFixture{ctx: ctx, store: st, playground: pg, extendedExpiry: extendedExpiry}
}

func assertPlaygroundExpiry(t *testing.T, context string, pg domain.Playground, want time.Time) {
	t.Helper()
	if pg.ExpiresAt == nil || !pg.ExpiresAt.Equal(want) {
		t.Fatalf("%s should preserve current expiry, got %#v want %s", context, pg.ExpiresAt, want)
	}
}

func assertObserveRuntimeTimeout(t *testing.T, project string, stdout string, label string) {
	t.Helper()
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_INSPECT": {Stdout: stdout},
		},
	}
	w := Worker{
		Runtime:                runtime.Checker{Executor: fake},
		RuntimeObserveTimeout:  5 * time.Millisecond,
		RuntimeObserveInterval: time.Millisecond,
	}
	pg := domain.Playground{ServiceURLs: []domain.PlaygroundServiceURL{{Name: "web"}}}
	_, err := w.observeRuntimeServices(context.Background(), domain.Marquee{}, project, pg)
	if err == nil || !strings.Contains(err.Error(), "did not become running and healthy") {
		t.Fatalf("expected %s to time out, got %v", label, err)
	}
}

type stopDuringRefreshRepairExecutor struct {
	runtimetest.FakeExecutor
	store        *store.DB
	playgroundID int64
	mutated      bool
}

func (e *stopDuringRefreshRepairExecutor) Run(ctx context.Context, _ domain.Marquee, command string) (runtime.CommandResult, error) {
	e.Seen = append(e.Seen, command)
	if strings.Contains(command, "ps --all --format json") {
		return runtime.CommandResult{}, errors.New("open /opt/fibe/playgrounds/repair-superseded--1/compose.yml: no such file or directory")
	}
	if !e.mutated && strings.Contains(command, " up -d --remove-orphans ") {
		e.mutated = true
		if err := markPlaygroundStopped(ctx, e.store, e.playgroundID); err != nil {
			return runtime.CommandResult{}, err
		}
	}
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *stopDuringRefreshRepairExecutor) Services(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) ([]domain.PlaygroundServiceInfo, error) {
	return inspectRunningWebService(ctx, e, marquee, project, base)
}

func (e *stopDuringRefreshRepairExecutor) Up(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	_, err := e.Run(ctx, marquee, runtimetest.ComposeUpCommand("", project, base, marquee.ID))
	return err
}

func (e *stopDuringRefreshRepairExecutor) Stop(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	_, err := e.Run(ctx, marquee, runtimetest.ComposeStopCommand("", project, base, marquee.ID))
	return err
}

type runningRefreshWithStaleErrorFixture struct {
	ctx        context.Context
	store      *store.DB
	worker     Worker
	playground domain.Playground
}

func newRunningRefreshWithStaleErrorFixture(t *testing.T) runningRefreshWithStaleErrorFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","Image":"nginx:alpine","State":"running","Health":"healthy","ExitCode":0}]`},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	project := "stale-error-running--1"
	message := "playground deployment was interrupted by fibe-distilled restart"
	reason := "runtime_observe"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "stale-error-running-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		ErrorMessage:   &message,
		ErrorDetails:   map[string]any{"interrupted": map[string]any{"code": "INTERRUPTED"}},
		StateReason:    &reason,
		StateReasons:   []string{reason},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return runningRefreshWithStaleErrorFixture{ctx: ctx, store: st, worker: w, playground: pg}
}

func assertRunningRefreshClearedStaleError(t *testing.T, fixture runningRefreshWithStaleErrorFixture, status domain.PlaygroundStatus) {
	t.Helper()
	if status.Status != domain.StatusRunning || status.ErrorMessage != nil {
		t.Fatalf("expected clean running status, got %#v", status)
	}
	persisted, err := fixture.store.GetPlayground(fixture.ctx, "stale-error-running-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.ErrorMessage != nil || len(persisted.ErrorDetails) != 0 || persisted.StateReason != nil || len(persisted.StateReasons) != 0 {
		t.Fatalf("refresh should clear stale error fields, got %#v", persisted)
	}
}

func openWorkerTestStore(t *testing.T) (context.Context, *store.DB) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return ctx, st
}

func assertSeenContainsAll(t *testing.T, seen []string, wants []string) {
	t.Helper()
	text := strings.Join(seen, "\n")
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("expected command %q in:\n%s", want, text)
		}
	}
}

func assertEmptyObservationStoppedService(t *testing.T, pg domain.Playground) {
	t.Helper()
	if len(pg.Services) != 1 || pg.Services[0].Name != "web" || pg.Services[0].Image != "nginx:alpine" {
		t.Fatalf("empty runtime observation should preserve stored service metadata, got %#v", pg)
	}
	if pg.Services[0].Running || pg.Services[0].Status != domain.StatusStopped {
		t.Fatalf("empty runtime observation should stop service metadata, got %#v", pg.Services[0])
	}
}

func assertStoppedServiceURL(t *testing.T, urls []domain.PlaygroundServiceURL, service string) {
	t.Helper()
	if len(urls) != 1 || urls[0].Name != service || urls[0].Status != domain.StatusStopped {
		t.Fatalf("empty runtime observation should stop service URLs for %s, got %#v", service, urls)
	}
	if urls[0].Running == nil || *urls[0].Running {
		t.Fatalf("empty runtime observation should set service URL running=false for %s, got %#v", service, urls[0])
	}
}

type dirtyRepairRefreshFixture struct {
	ctx        context.Context
	store      *store.DB
	fake       *runtimetest.FakeExecutor
	worker     Worker
	playground domain.Playground
}

func newDirtyRepairRefreshFixture(t *testing.T) dirtyRepairRefreshFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "dirty-repair--1"
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"ps --all --format json":     errors.New("open /opt/fibe/playgrounds/dirty-repair--1/compose.yml: no such file or directory"),
			"FIBE_DISTILLED_SOURCE_SYNC": errors.New("source sync refused dirty work"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_SOURCE_SYNC": {Stderr: "fibe_distilled_source_sync_category=dirty_work\nfibe_distilled_source_sync_dirty_entries=2\n"},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "dirty-repair-spec",
		BaseComposeYAML: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
`,
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:                 "dirty-repair-pg",
		Status:               domain.StatusRunning,
		PlayspecID:           ps.ID,
		MarqueeID:            &mq.ID,
		ComposeProject:       &project,
		GeneratedComposeYAML: ps.BaseComposeYAML,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return dirtyRepairRefreshFixture{ctx: ctx, store: st, fake: fake, worker: w, playground: pg}
}

func assertDirtyRepairSkippedRedeploy(t *testing.T, fixture dirtyRepairRefreshFixture, status domain.PlaygroundStatus) {
	t.Helper()
	if status.Status != domain.StatusHasChanges {
		t.Fatalf("expected dirty repair skip to become has_changes, got %#v", status)
	}
	updated, err := fixture.store.GetPlayground(fixture.ctx, "dirty-repair-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if updated.StateReason == nil || *updated.StateReason != "source_sync_dirty_work" {
		t.Fatalf("expected dirty source reason, got %#v", updated)
	}
	seen := strings.Join(fixture.fake.Seen, "\n")
	if strings.Contains(seen, " up -d --remove-orphans --pull missing") {
		t.Fatalf("dirty source repair must not redeploy compose:\n%s", seen)
	}
}

func createRefreshDriftFixture(ctx context.Context, t *testing.T, st *store.DB, specName, playgroundName, project, composeYAML, image string) domain.Playground {
	t.Helper()
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{Name: specName, BaseComposeYAML: composeYAML})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:                 playgroundName,
		Status:               domain.StatusRunning,
		PlayspecID:           ps.ID,
		MarqueeID:            &mq.ID,
		ComposeProject:       &project,
		GeneratedComposeYAML: ps.BaseComposeYAML,
		Services:             []domain.PlaygroundServiceInfo{{Name: "web", Status: "running", Image: image, Running: true}},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return pg
}

type dirtySourceSyncReconcileFixture struct {
	ctx    context.Context
	store  *store.DB
	worker Worker
}

func newDirtySourceSyncReconcileFixture(t *testing.T) dirtySourceSyncReconcileFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{"FIBE_DISTILLED_SOURCE_SYNC": errors.New("source sync refused dirty work")},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_SOURCE_SYNC": {Stderr: "fibe_distilled_source_sync_category=dirty_work\nfibe_distilled_source_sync_dirty_entries=1\n"},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "source-sync-spec",
		BaseComposeYAML: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
`,
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	project := "source-sync--1"
	_, err = st.CreatePlayground(ctx, domain.Playground{
		Name:           "source-sync-pg",
		Status:         domain.StatusRunning,
		PlayspecID:     ps.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return dirtySourceSyncReconcileFixture{ctx: ctx, store: st, worker: w}
}

func assertDirtySourceSyncPreservedWork(t *testing.T, fixture dirtySourceSyncReconcileFixture) {
	t.Helper()
	updated, err := fixture.store.GetPlayground(fixture.ctx, "source-sync-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if updated.Status != domain.StatusHasChanges || updated.StateReason == nil || *updated.StateReason != "source_sync_dirty_work" {
		t.Fatalf("expected dirty source sync to preserve work as has_changes, got %#v", updated)
	}
	details := updated.ErrorDetails["source_sync"].(map[string]any)
	if details["category"] != "source_sync_dirty_work" || !strings.Contains(details["path"].(string), "/opt/fibe/playgrounds/source-sync--1/props/acme-demo/main") {
		t.Fatalf("unexpected source sync details: %#v", details)
	}
}

func createSourceSyncPlayground(ctx context.Context, t *testing.T, st *store.DB, name string, status string, project string) domain.Playground {
	t.Helper()
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: name + "-spec",
		BaseComposeYAML: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://example.com/acme/demo.git
      fibe.gg/source_mount: /app
`,
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           name + "-pg",
		Status:         status,
		PlayspecID:     ps.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return pg
}

type stopDuringSourceSyncExecutor struct {
	store        *store.DB
	playgroundID int64
	mutated      bool
}

func (e *stopDuringSourceSyncExecutor) Run(ctx context.Context, _ domain.Marquee, command string) (runtime.CommandResult, error) {
	if !e.mutated && strings.Contains(command, "FIBE_DISTILLED_SOURCE_SYNC") {
		e.mutated = true
		if err := markPlaygroundStopped(ctx, e.store, e.playgroundID); err != nil {
			return runtime.CommandResult{}, err
		}
	}
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *stopDuringSourceSyncExecutor) WriteFile(context.Context, domain.Marquee, string, string) (runtime.CommandResult, error) {
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *stopDuringSourceSyncExecutor) Sync(ctx context.Context, _ domain.Marquee, _ runtime.GitSyncRequest) error {
	if !e.mutated {
		e.mutated = true
		if err := markPlaygroundStopped(ctx, e.store, e.playgroundID); err != nil {
			return err
		}
	}
	return nil
}

func (e *stopDuringSourceSyncExecutor) DirtyPaths(context.Context, domain.Marquee, string, []string) ([]string, error) {
	return nil, nil
}

func (e *stopDuringSourceSyncExecutor) Head(context.Context, domain.Marquee, string, string) (string, error) {
	return "abcdef1234567890", nil
}

func assertCreationStep(t *testing.T, steps []domain.PlaygroundCreationStep, name string, status string) {
	t.Helper()
	for _, step := range steps {
		if step.Name != name {
			continue
		}
		if step.Status != status || step.StartedAt == nil || step.CompletedAt == nil {
			t.Fatalf("unexpected step %s: %#v", name, step)
		}
		return
	}
	t.Fatalf("missing creation step %s in %#v", name, steps)
}

func assertCreationFailureStatus(t *testing.T, pg domain.Playground, step string, label string) {
	t.Helper()
	assertCreationStep(t, pg.CreationSteps, step, "error")
	status := statusFromPlayground(pg)
	if status.ErrorStep != step || status.ErrorStepLabel != label {
		t.Fatalf("unexpected failed creation status: %#v", status)
	}
}

func assertSourceSyncNotSeen(t *testing.T, seen []string, reason string) {
	t.Helper()
	for _, command := range seen {
		if strings.Contains(command, "FIBE_DISTILLED_SOURCE_SYNC") {
			t.Fatalf("source sync should not run after %s:\n%s", reason, strings.Join(seen, "\n"))
		}
	}
}

func assertActiveDemoBuildStatus(t *testing.T, pg domain.Playground) {
	t.Helper()
	active := activeDemoBuildSnapshot(t, pg)
	assertDemoBuildSnapshotCommit(t, active)
}

func activeDemoBuildSnapshot(t *testing.T, pg domain.Playground) *domain.PlaygroundBuildRecordSnapshot {
	t.Helper()
	if len(pg.BuildStatuses) != 1 {
		t.Fatalf("expected one build status: %#v", pg.BuildStatuses)
	}
	if pg.BuildStatuses[0].Active == nil {
		t.Fatalf("expected active build status: %#v", pg.BuildStatuses)
	}
	return pg.BuildStatuses[0].Active
}

func assertDemoBuildSnapshotCommit(t *testing.T, active *domain.PlaygroundBuildRecordSnapshot) {
	t.Helper()
	if active.Status != "success" || active.CommitSHA != "abcdef1234567890" {
		t.Fatalf("unexpected active build: %#v", active)
	}
}

func assertComposeUsesDemoBuiltImage(t *testing.T, composeYAML string) {
	t.Helper()
	if !strings.Contains(composeYAML, "image: fibe-distilled/demo--42/web:abcdef1234567890") || strings.Contains(composeYAML, "build:") {
		t.Fatalf("expected generated compose to consume built image:\n%s", composeYAML)
	}
}

func assertReusedBuildSnapshot(t *testing.T, pg domain.Playground, imageRef string) {
	t.Helper()
	active := activeDemoBuildSnapshot(t, pg)
	if active.Status != "success" || active.CommitSHA != "abcdef1234567890" || active.ImageRef != imageRef {
		t.Fatalf("expected reused active build snapshot: %#v", active)
	}
}

func assertReusedBuildRecord(t *testing.T, ctx context.Context, st *store.DB, dbPath string, pg domain.Playground, playgroundID int64, propID int64, imageRef string) {
	t.Helper()
	assertBuildRecordCount(t, ctx, dbPath, playgroundID, 1)
	record := activeBuildRecord(t, ctx, st, pg)
	if record.Status != "success" || !record.Reused || record.ImageRef != imageRef || record.CommitSHA != "abcdef1234567890" {
		t.Fatalf("expected reused build record: %#v", record)
	}
	if record.PropID == nil || *record.PropID != propID {
		t.Fatalf("expected reused build record to keep prop identity: %#v", record)
	}
}

func assertReusedComposeImage(t *testing.T, composeYAML string, imageRef string) {
	t.Helper()
	if !strings.Contains(composeYAML, "image: "+imageRef) || strings.Contains(composeYAML, "build:") {
		t.Fatalf("expected generated compose to consume reused image:\n%s", composeYAML)
	}
}

func assertRemoteBuildNotSeen(t *testing.T, fake *runtimetest.FakeExecutor) {
	t.Helper()
	seen := strings.Join(fake.Seen, "\n")
	if strings.Contains(seen, " /opt/fibe/builds/remote_build.sh ") {
		t.Fatalf("reused build must not run local build command:\n%s", seen)
	}
	if !strings.Contains(seen, "docker image inspect") {
		t.Fatalf("reused build must be verified with image inspect:\n%s", seen)
	}
}

func assertPersistedDemoBuildRecord(t *testing.T, ctx context.Context, st *store.DB, dbPath string, pg domain.Playground, playgroundID int64) {
	t.Helper()
	assertBuildRecordCount(t, ctx, dbPath, playgroundID, 1)
	record := activeBuildRecord(t, ctx, st, pg)
	if record.Status != "success" || record.ImageRef != "fibe-distilled/demo--42/web:abcdef1234567890" {
		t.Fatalf("unexpected record: %#v", record)
	}
	if record.BuildDockerfilePath != "Dockerfile.web" || record.BuildTarget != "prod" || record.BuildArgsDigest == "" || record.BuildIdentityDigest == "" {
		t.Fatalf("expected persisted build identity: %#v", record)
	}
	if record.BuildPlatform != "linux/amd64" || record.BuildCacheKey != record.BuildIdentityDigest {
		t.Fatalf("expected persisted build platform/cache key: %#v", record)
	}
}

func assertInternalAuthDeploy(t *testing.T, pg domain.Playground) {
	t.Helper()
	assertGeneratedInternalPassword(t, pg)
	assertObservedInternalAuthService(t, pg)
	assertObservedInternalAuthURL(t, pg)
	assertInternalAuthCompose(t, pg)
}

func assertGeneratedInternalPassword(t *testing.T, pg domain.Playground) {
	t.Helper()
	if pg.InternalPassword == nil || len(*pg.InternalPassword) != 24 {
		t.Fatalf("expected generated 24-character internal password, got %#v", pg.InternalPassword)
	}
}

func assertObservedInternalAuthService(t *testing.T, pg domain.Playground) {
	t.Helper()
	if len(pg.Services) != 1 || pg.Services[0].Status != "running" || pg.Services[0].Health != "healthy" {
		t.Fatalf("expected deploy to save observed service status: %#v", pg.Services)
	}
}

func assertObservedInternalAuthURL(t *testing.T, pg domain.Playground) {
	t.Helper()
	if len(pg.ServiceURLs) != 1 || pg.ServiceURLs[0].Status != "running" || pg.ServiceURLs[0].Health != "healthy" || pg.ServiceURLs[0].Running == nil || !*pg.ServiceURLs[0].Running {
		t.Fatalf("expected deploy to mirror observed status onto service URL: %#v", pg.ServiceURLs)
	}
}

func assertInternalAuthCompose(t *testing.T, pg domain.Playground) {
	t.Helper()
	for _, want := range []string{
		"traefik.http.routers.auth--1-web-http.middlewares: auth--1-web-redirect",
		"traefik.http.routers.auth--1-web-secure.middlewares: auth--1-web-auth",
		"traefik.http.middlewares.auth--1-web-auth.basicauth.users: playground:$$2",
		"-" + strconv.FormatInt(pg.ID, 10),
	} {
		if !strings.Contains(pg.GeneratedComposeYAML, want) {
			t.Fatalf("expected %q in generated compose:\n%s", want, pg.GeneratedComposeYAML)
		}
	}
	if strings.Contains(pg.GeneratedComposeYAML, *pg.InternalPassword) || strings.Contains(pg.GeneratedComposeYAML, "service-password") {
		t.Fatalf("generated compose must not contain raw internal password:\n%s", pg.GeneratedComposeYAML)
	}
	hash := basicAuthHashFromCompose(t, pg.GeneratedComposeYAML, "auth--1-web-auth", pg.ID)
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("service-password")); err != nil {
		t.Fatalf("auth hash should match service auth_password: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(*pg.InternalPassword)); err == nil {
		t.Fatalf("auth hash unexpectedly matched the playground-level password")
	}
}

func assertPersistedInternalAuthDeploy(t *testing.T, persisted domain.Playground, updated domain.Playground) {
	t.Helper()
	if persisted.InternalPassword == nil || *persisted.InternalPassword != *updated.InternalPassword {
		t.Fatalf("expected persisted internal password, got %#v", persisted.InternalPassword)
	}
	for _, step := range []string{"compose_render", "host_prerequisites", "source_sync", "builds", "compose_deploy", "runtime_observe", "finalize"} {
		assertCreationStep(t, persisted.CreationSteps, step, "completed")
	}
	status := statusFromPlayground(persisted)
	if status.CreationStep != "completed" || status.CreationStepLabel != "Completed" || len(status.CreationSteps) == 0 {
		t.Fatalf("unexpected creation status: %#v", status)
	}
}

func createTestRuntimeMarquee(t *testing.T, ctx context.Context, st *store.DB, m domain.Marquee) (domain.Marquee, error) {
	t.Helper()
	m.Name = store.ConfiguredMarqueeName
	return st.EnsureConfiguredMarquee(ctx, m)
}

func assertBuildRecordCount(t *testing.T, ctx context.Context, dbPath string, playgroundID int64, want int) {
	t.Helper()
	if got := buildRecordCountForPlayground(t, ctx, dbPath, playgroundID); got != want {
		t.Fatalf("build record count = %d, want %d", got, want)
	}
}

func buildRecordCountForPlayground(t *testing.T, ctx context.Context, dbPath string, playgroundID int64) int {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open assertion db: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close assertion db: %v", err)
		}
	}()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM build_records WHERE playground_id=?`, playgroundID).Scan(&count); err != nil {
		t.Fatalf("count build records: %v", err)
	}
	return count
}

func activeBuildRecord(t *testing.T, ctx context.Context, st *store.DB, pg domain.Playground) domain.BuildRecord {
	t.Helper()
	active := activeDemoBuildSnapshot(t, pg)
	record, err := st.GetBuildRecord(ctx, active.ID)
	if err != nil {
		t.Fatalf("get active build record: %v", err)
	}
	return record
}

func assertExecutorSaw(t *testing.T, fake *runtimetest.FakeExecutor, want string, context string) {
	t.Helper()
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, want) {
		t.Fatalf("expected %s:\n%s", context, seen)
	}
}

func assertRuntimeWorkspacePreparedBeforeSourceSync(t *testing.T, seen []string, project string) {
	t.Helper()
	dockerConfig := "write:/opt/fibe/playgrounds/" + project + "/docker-config/config.json:"
	sourceSync := "FIBE_DISTILLED_SOURCE_SYNC"
	assertSeenOrder(t, seen, dockerConfig, sourceSync)
	joined := strings.Join(seen, "\n")
	if strings.Contains(joined, "write:/opt/fibe/playgrounds/"+project+"/compose.yml:") {
		t.Fatalf("failed source sync should not write compose.yml:\n%s", joined)
	}
}

func assertSeenOrder(t *testing.T, seen []string, first string, second string) {
	t.Helper()
	firstIndex, secondIndex := -1, -1
	for i, entry := range seen {
		if firstIndex == -1 && strings.Contains(entry, first) {
			firstIndex = i
		}
		if secondIndex == -1 && strings.Contains(entry, second) {
			secondIndex = i
		}
	}
	if firstIndex == -1 || secondIndex == -1 || firstIndex >= secondIndex {
		t.Fatalf("expected %q before %q in runtime calls:\n%s", first, second, strings.Join(seen, "\n"))
	}
}

func assertSourceSyncFailureDetailsRedacted(t *testing.T, errorDetails map[string]any) {
	t.Helper()
	sourceDetails, ok := errorDetails["source_sync"].(map[string]any)
	if !ok {
		t.Fatalf("expected persisted source_sync details, got %#v", errorDetails)
	}
	detailsText := fmt.Sprint(sourceDetails)
	if strings.Contains(detailsText, "ghp_secret") || strings.Contains(detailsText, "eC1hY2Nlc3MtdG9rZW46Z2hwX3NlY3JldA==") {
		t.Fatalf("persisted source-sync diagnostics leaked credentials: %s", detailsText)
	}
	if !strings.Contains(detailsText, "https://***@github.com/acme/demo.git") || !strings.Contains(detailsText, "Authorization: Basic ***") {
		t.Fatalf("expected redacted diagnostics in persisted details, got %s", detailsText)
	}
}

func assertSourceSyncFailureStatus(t *testing.T, persisted domain.Playground) {
	t.Helper()
	assertCreationStep(t, persisted.CreationSteps, "compose_render", "completed")
	assertCreationStep(t, persisted.CreationSteps, "source_sync", "error")
	status := statusFromPlayground(persisted)
	if status.CreationStep != "source_sync" || status.ErrorStep != "source_sync" || status.ErrorStepLabel != "Sync sources" {
		t.Fatalf("unexpected failed creation status: %#v", status)
	}
}

func basicAuthHashFromCompose(t *testing.T, rendered string, middleware string, playgroundID int64) string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v", err)
	}
	services := doc["services"].(map[string]any)
	web := services["web"].(map[string]any)
	labels := web["labels"].(map[string]any)
	users := labels["traefik.http.middlewares."+middleware+".basicauth.users"].(string)
	hash := strings.TrimPrefix(users, "playground:")
	hash = strings.TrimSuffix(hash, "-"+strconv.FormatInt(playgroundID, 10))
	hash = strings.ReplaceAll(hash, "$$", "$")
	return hash
}

func createExpiringSourcePlayground(ctx context.Context, t *testing.T, st *store.DB, name string, project string, expiresAt time.Time) domain.Playground {
	t.Helper()
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: name + "-spec",
		BaseComposeYAML: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
`,
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: name + "-marquee", Host: "127.0.0.1", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           name,
		Status:         domain.StatusRunning,
		PlayspecID:     ps.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		ExpiresAt:      &expiresAt,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return pg
}

type mutateDuringExpirationDirtyCheckExecutor struct {
	runtimetest.FakeExecutor
	store        *store.DB
	playgroundID int64
	stdout       string
	mutateOn     string
	mutate       func(context.Context, *store.DB, int64) error
	mutated      bool
}

func (e *mutateDuringExpirationDirtyCheckExecutor) Run(ctx context.Context, _ domain.Marquee, command string) (runtime.CommandResult, error) {
	e.Seen = append(e.Seen, command)
	if strings.Contains(command, "ps --all --format json") {
		return runtime.CommandResult{Stdout: `[{"Service":"web","Image":"alpine","State":"running","Health":"healthy","ExitCode":0}]`}, nil
	}
	trigger := e.mutateOn
	if trigger == "" {
		trigger = "git -C "
	}
	if !e.mutated && strings.Contains(command, trigger) {
		e.mutated = true
		if e.mutate != nil {
			if err := e.mutate(ctx, e.store, e.playgroundID); err != nil {
				return runtime.CommandResult{}, err
			}
		}
		return runtime.CommandResult{Stdout: e.stdout}, nil
	}
	if strings.Contains(command, "git -C ") {
		return runtime.CommandResult{Stdout: e.stdout}, nil
	}
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *mutateDuringExpirationDirtyCheckExecutor) DirtyPaths(ctx context.Context, marquee domain.Marquee, project string, paths []string) ([]string, error) {
	var dirty []string
	for _, sourcePath := range paths {
		command := "FIBE_DISTILLED_SOURCE_DIRTY " + project + " " + sourcePath
		result, err := e.runDirtyCommand(ctx, marquee, command)
		if err != nil || strings.TrimSpace(result.Stdout+"\n"+result.Stderr) != "" {
			dirty = append(dirty, sourcePath)
		}
	}
	return dirty, nil
}

func (e *mutateDuringExpirationDirtyCheckExecutor) Sync(context.Context, domain.Marquee, runtime.GitSyncRequest) error {
	return nil
}

func (e *mutateDuringExpirationDirtyCheckExecutor) Head(context.Context, domain.Marquee, string, string) (string, error) {
	return "abcdef1234567890", nil
}

func (e *mutateDuringExpirationDirtyCheckExecutor) Down(ctx context.Context, marquee domain.Marquee, project string, base string, _ string, removeVolumes bool) error {
	args := "down --remove-orphans"
	if removeVolumes {
		args += " -v"
	}
	_, err := e.Run(ctx, marquee, "cd "+runtime.ShellQuote(base)+" && docker compose -f compose.yml -p "+runtime.ShellQuote(project)+" "+args)
	return err
}

func (e *mutateDuringExpirationDirtyCheckExecutor) runDirtyCommand(ctx context.Context, marquee domain.Marquee, command string) (runtime.CommandResult, error) {
	trigger := e.mutateOn
	if trigger == "" {
		trigger = "FIBE_DISTILLED_SOURCE_DIRTY"
	}
	if !e.mutated && strings.Contains(command, trigger) {
		e.mutated = true
		if e.mutate != nil {
			if err := e.mutate(ctx, e.store, e.playgroundID); err != nil {
				return runtime.CommandResult{}, err
			}
		}
		e.Seen = append(e.Seen, command)
		return runtime.CommandResult{Stdout: e.stdout}, nil
	}
	_ = marquee
	e.Seen = append(e.Seen, command)
	return runtime.CommandResult{Stdout: e.stdout}, nil
}

type missingPlayspecFixture struct {
	ctx    context.Context
	dbPath string
	store  *store.DB
	fake   *runtimetest.FakeExecutor
	worker Worker
}

func newMissingPlayspecFixture(t *testing.T, name string, project string, expiresAt *time.Time, fake *runtimetest.FakeExecutor) missingPlayspecFixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ps := createMissingPlayspecFixtureSpec(t, ctx, st, name)
	mq := createMissingPlayspecFixtureMarquee(t, ctx, st, name)
	createMissingPlayspecFixturePlayground(t, ctx, st, name, project, ps, mq, expiresAt)
	return missingPlayspecFixture{
		ctx:    ctx,
		dbPath: dbPath,
		store:  st,
		fake:   fake,
		worker: Worker{DB: st, Runtime: runtime.Checker{Executor: fake}},
	}
}

func createMissingPlayspecFixtureSpec(t *testing.T, ctx context.Context, st *store.DB, name string) domain.Playspec {
	t.Helper()
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{Name: name + "-spec", BaseComposeYAML: missingPlayspecFixtureComposeYAML()})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	return ps
}

func createMissingPlayspecFixtureMarquee(t *testing.T, ctx context.Context, st *store.DB, name string) domain.Marquee {
	t.Helper()
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: name + "-marquee", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	return mq
}

func createMissingPlayspecFixturePlayground(t *testing.T, ctx context.Context, st *store.DB, name string, project string, ps domain.Playspec, mq domain.Marquee, expiresAt *time.Time) {
	t.Helper()
	if _, err := st.CreatePlayground(ctx, domain.Playground{
		Name:                 name,
		Status:               domain.StatusRunning,
		PlayspecID:           ps.ID,
		MarqueeID:            &mq.ID,
		ComposeProject:       &project,
		GeneratedComposeYAML: ps.BaseComposeYAML,
		ExpiresAt:            expiresAt,
	}); err != nil {
		t.Fatalf("create playground: %v", err)
	}
}

func missingPlayspecFixtureComposeYAML() string {
	return `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
`
}

func corruptFixturePlayspecID(t *testing.T, fixture missingPlayspecFixture, playgroundName string) domain.Playground {
	t.Helper()
	db, err := sql.Open("sqlite3", fixture.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer closeSQLDB(t, db)
	pg, err := fixture.store.GetPlayground(fixture.ctx, playgroundName)
	if err != nil {
		t.Fatalf("get playground before corruption: %v", err)
	}
	if _, err := db.ExecContext(fixture.ctx, `UPDATE playgrounds SET playspec_id=? WHERE id=?`, int64(99999), pg.ID); err != nil {
		t.Fatalf("corrupt playground playspec reference: %v", err)
	}
	corrupt, err := fixture.store.GetPlayground(fixture.ctx, playgroundName)
	if err != nil {
		t.Fatalf("get corrupted playground: %v", err)
	}
	return corrupt
}

func closeStore(t *testing.T, st *store.DB) {
	t.Helper()
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func closeSQLDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}
}

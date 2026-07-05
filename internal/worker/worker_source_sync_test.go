package worker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

func TestSyncSourcesUsesFibePropSlugAndFailsClosed(t *testing.T) {
	fake := &runtimetest.FakeExecutor{}
	w := Worker{Runtime: runtime.Checker{Executor: fake}, DefaultGitHubToken: "secret-token"}
	branch := "feature/new-ui"
	ps := domain.Playspec{BaseComposeYAML: `services:
  test:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/my-api.git
      fibe.gg/source_mount: /app
      fibe.gg/branch: ` + branch + `
`}
	err := w.syncSources(context.Background(), domain.Marquee{Name: "local"}, "demo--42", ps)
	if err != nil {
		t.Fatalf("sync sources: %v", err)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "/opt/fibe/playgrounds/demo--42/props/acme-my-api/feature-new-ui") {
		t.Fatalf("expected Fibe-compatible source path, got:\n%s", seen)
	}
	assertSourceSyncCommandExcludesUnsafeFallbacks(t, seen)
	assertSourceSyncCommandIncludesTypedRequest(t, seen, branch)
	if strings.Contains(seen, "x-access-token:secret-token@github.com") ||
		strings.Contains(seen, "remote set-url origin \"$repo\"") && strings.Contains(seen, "repo='https://x-access-token:") {
		t.Fatalf("source sync must not persist credentialed origin URLs:\n%s", seen)
	}
}

func TestSyncSourcesRejectsBeforeRemoteCommand(t *testing.T) {
	cases := []struct {
		name          string
		project       string
		composeYAML   string
		errorContains string
	}{
		{
			name:          "invalid compose",
			project:       "invalid-source--1",
			composeYAML:   "services:\n  web:\n",
			errorContains: "validate compose for source sync",
		},
		{
			name:    "unsafe project",
			project: "../other",
			composeYAML: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
`,
			errorContains: "unsafe compose project",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			fake := &runtimetest.FakeExecutor{}
			w := Worker{Runtime: runtime.Checker{Executor: fake}}
			err := w.syncSources(context.Background(), domain.Marquee{Name: "local"}, tt.project, domain.Playspec{BaseComposeYAML: tt.composeYAML})
			if err == nil || !strings.Contains(err.Error(), tt.errorContains) {
				t.Fatalf("expected %q error, got %v", tt.errorContains, err)
			}
			assertSourceSyncRemoteCommandsNotRun(t, fake, tt.name)
		})
	}
}

func TestSourceSyncCommandStripsCredentialedRemoteURL(t *testing.T) {
	command := sourceSyncCommand(
		runtimetest.MustRemoteCheckoutPath(t, "demo--1", "/opt/fibe/playgrounds/demo--1/props/acme-private/main"),
		"https://x-access-token:ghp_secret@github.com/acme/private.git",
		"Authorization: Basic secret-header",
		"main",
	)
	if strings.Contains(command, "ghp_secret") || strings.Contains(command, "x-access-token:") {
		t.Fatalf("source sync command leaked credentialed remote URL:\n%s", command)
	}
	if !strings.Contains(command, "repo='https://github.com/acme/private.git'") {
		t.Fatalf("source sync command should use bare remote URL:\n%s", command)
	}
}

func TestSourceSyncCommandShellParses(t *testing.T) {
	command := sourceSyncCommand(
		runtimetest.MustRemoteCheckoutPath(t, "demo--1", "/opt/fibe/playgrounds/demo--1/props/acme-private/main"),
		"https://github.com/acme/private.git",
		"Authorization: Basic secret-header",
		"main",
	)
	result := exec.Command("sh", "-n", "-c", command)
	output, err := result.CombinedOutput()
	if err != nil {
		t.Fatalf("source sync shell does not parse: %v\n%s\ncommand:\n%s", err, output, command)
	}
}

func TestSourceSyncScriptStopsWhenHistoryCannotBeVerified(t *testing.T) {
	for _, tc := range []struct {
		name       string
		revListCmd string
	}{
		{name: "failed command", revListCmd: `exit 9`},
		{name: "blank output", revListCmd: `exit 0`},
		{name: "single count", revListCmd: `echo "1"; exit 0`},
		{name: "extra count", revListCmd: `echo "0 0 0"; exit 0`},
		{name: "non numeric", revListCmd: `echo "x 0"; exit 0`},
		{name: "colon count", revListCmd: `echo "1:2 0"; exit 0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, exitCode := runSourceSyncScriptWithRevList(t, tc.revListCmd)
			if exitCode != 25 {
				t.Fatalf("expected exit 25, got %d output:\n%s", exitCode, output)
			}
			if !strings.Contains(output, "fibe_distilled_source_sync_category=history_unverifiable") {
				t.Fatalf("expected history marker, output:\n%s", output)
			}
			if strings.Contains(output, "pull ok") {
				t.Fatalf("source sync attempted pull after unverifiable history:\n%s", output)
			}
		})
	}
}

func TestSourceSyncScriptClassifiesHugeHistoryCountsWithoutShellArithmetic(t *testing.T) {
	huge := strings.Repeat("9", 80)
	for _, tc := range []struct {
		name       string
		revListOut string
		wantExit   int
		wantOutput string
	}{
		{name: "huge clean", revListOut: "0 0", wantExit: 0, wantOutput: "pull ok"},
		{name: "huge ahead", revListOut: huge + " 0", wantExit: 24, wantOutput: "source_sync_category=ahead"},
		{name: "huge diverged", revListOut: huge + " " + huge, wantExit: 23, wantOutput: "source_sync_category=diverged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, exitCode := runSourceSyncScriptWithRevList(t, `echo "`+tc.revListOut+`"; exit 0`)
			if exitCode != tc.wantExit {
				t.Fatalf("exit code = %d, want %d, output:\n%s", exitCode, tc.wantExit, output)
			}
			if !strings.Contains(output, tc.wantOutput) {
				t.Fatalf("expected %q in output:\n%s", tc.wantOutput, output)
			}
			if strings.Contains(output, "integer expression expected") {
				t.Fatalf("source sync should not use shell integer parsing for counts:\n%s", output)
			}
		})
	}
}

func TestSourceSyncScriptReportsDirtyEntryCount(t *testing.T) {
	output, exitCode := runSourceSyncScript(
		t,
		`printf '%s\n' ' M app.go' '?? README.md'; exit 0`,
		`echo "0 0"; exit 0`,
	)
	if exitCode != 20 {
		t.Fatalf("expected dirty-work exit 20, got %d output:\n%s", exitCode, output)
	}
	if !strings.Contains(output, "fibe_distilled_source_sync_category=dirty_work") ||
		!strings.Contains(output, "fibe_distilled_source_sync_dirty_entries=2") {
		t.Fatalf("expected two dirty entries in source sync output:\n%s", output)
	}
}

func TestSourceSyncScriptDoesNotChangeOriginWhenWorktreeIsDirty(t *testing.T) {
	output, exitCode := runSourceSyncScriptWithGitScript(t, `#!/bin/sh
case "$*" in
  *"status --porcelain"*) printf '%s\n' ' M app.go'; exit 0 ;;
  *"remote set-url origin"*) echo "remote-set-url-called" >&2; exit 0 ;;
esac
exit 0
`)
	if exitCode != 20 {
		t.Fatalf("expected dirty-work exit 20, got %d output:\n%s", exitCode, output)
	}
	if strings.Contains(output, "remote-set-url-called") {
		t.Fatalf("dirty source sync changed origin before preserving work:\n%s", output)
	}
}

func TestSyncSourcesUsesProcessGitHubTokenOnly(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}, DefaultGitHubToken: "process-token"}
	ps := domain.Playspec{BaseComposeYAML: `services:
  web:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/private.git
      fibe.gg/source_mount: /app
`}

	if err := w.syncSources(ctx, domain.Marquee{Name: "local"}, "token-process--1", ps); err != nil {
		t.Fatalf("sync sources should use process token without token storage: %v", err)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "github_auth=true") || strings.Contains(seen, "process-token") {
		t.Fatalf("expected source sync command to use process token auth header:\n%s", seen)
	}
}

func TestReloadPlaygroundAfterSourceSyncSkipsDeletedRows(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w := Worker{DB: st}

	reloaded, err := w.reloadPlaygroundAfterSourceSync(ctx, domain.Playground{ID: 123, Name: "deleted"})
	if err != nil {
		t.Fatalf("deleted row should be skipped, got %v", err)
	}
	if reloaded.ID != 0 {
		t.Fatalf("deleted row should return zero playground, got %#v", reloaded)
	}
}

func TestReloadPlaygroundAfterSourceSyncFailsClosedOnStoreError(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	w := Worker{DB: st}
	pg := domain.Playground{ID: 123, Name: "stale"}

	reloaded, err := w.reloadPlaygroundAfterSourceSync(ctx, pg)
	if err == nil {
		t.Fatal("closed store should fail instead of refreshing stale playground state")
	}
	if reloaded.ID != pg.ID {
		t.Fatalf("store error should preserve input playground for diagnostics, got %#v", reloaded)
	}
}

func TestSyncPlaygroundSourcesIfNeededWithoutStoreDoesNotPanic(t *testing.T) {
	marqueeID := int64(1)
	project := "source-sync-no-store--1"
	pg := domain.Playground{
		ID:             123,
		Name:           "source-sync-no-store",
		Status:         domain.StatusRunning,
		MarqueeID:      &marqueeID,
		ComposeProject: &project,
	}

	reloaded, err := (Worker{}).syncPlaygroundSourcesIfNeeded(context.Background(), pg)
	if err != nil {
		t.Fatalf("nil store source sync should no-op, got %v", err)
	}
	if reloaded.ID != pg.ID {
		t.Fatalf("nil store source sync should preserve playground, got %#v", reloaded)
	}
}

func TestSyncSourcesClassifiesDirtyWork(t *testing.T) {
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{"FIBE_DISTILLED_SOURCE_SYNC": errors.New("source sync failed")},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_SOURCE_SYNC": {Stderr: "fibe_distilled_source_sync_category=dirty_work\nfibe_distilled_source_sync_dirty_entries=2\n"},
		},
	}
	w := Worker{Runtime: runtime.Checker{Executor: fake}}
	ps := domain.Playspec{BaseComposeYAML: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
`}
	err := w.syncSources(context.Background(), domain.Marquee{Name: "local"}, "demo--1", ps)
	var syncErr sourceSyncError
	if !errors.As(err, &syncErr) {
		t.Fatalf("expected sourceSyncError, got %T %[1]v", err)
	}
	if syncErr.Category != "source_sync_dirty_work" || !syncErr.PreservesWork() || syncErr.Service != "web" {
		t.Fatalf("unexpected source sync error: %#v", syncErr)
	}
}

func TestSourceSyncClassifiesUnverifiableHistoryAsPreservedWork(t *testing.T) {
	err := classifySourceSyncError("web", "https://github.com/acme/demo.git", "main", "/opt/fibe/source", runtime.CommandResult{
		Stderr: "fibe_distilled_source_sync_category=history_unverifiable\n",
	}, errors.New("rev-list failed"))
	var syncErr sourceSyncError
	if !errors.As(err, &syncErr) {
		t.Fatalf("expected sourceSyncError, got %T %[1]v", err)
	}
	if syncErr.Category != "source_sync_history_unverifiable" || !syncErr.PreservesWork() {
		t.Fatalf("history verification failure must preserve work, got %#v", syncErr)
	}
	if !strings.Contains(syncErr.Message, "branch history could not be verified") {
		t.Fatalf("unexpected message: %q", syncErr.Message)
	}
	if got := strings.Join(syncErr.NextActions(), " "); !strings.Contains(got, "inspect the source checkout") {
		t.Fatalf("unexpected next actions: %#v", syncErr.NextActions())
	}
}

func TestSourceSyncErrorRedactsGitCredentials(t *testing.T) {
	syncErr := redactedCredentialSourceSyncError(t)
	assertSourceSyncDiagnosticsRedacted(t, syncErr)
	assertSourceSyncOutputRedacted(t, syncErr.Output)
}

func TestSourceSyncClaimsCurrentRowBeforeRemoteCommand(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	pg := createSourceSyncPlayground(ctx, t, st, "source-sync-current-row", domain.StatusRunning, "source-sync-old--1")
	stale := pg
	stale.MarqueeID = nil
	stale.PlayspecID = nil
	stale.ComposeProject = nil
	newProject := "source-sync-new--1"
	pg.ComposeProject = &newProject
	if _, err := st.SavePlayground(ctx, pg); err != nil {
		t.Fatalf("save current playground: %v", err)
	}

	if err := w.syncPlaygroundSources(ctx, stale); err != nil {
		t.Fatalf("sync playground sources: %v", err)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "/opt/fibe/playgrounds/source-sync-new--1/props/acme-demo/main") {
		t.Fatalf("source sync should use current row project:\n%s", seen)
	}
	if strings.Contains(seen, "/opt/fibe/playgrounds/source-sync-old--1/") {
		t.Fatalf("source sync used stale row project:\n%s", seen)
	}
}

func TestSourceSyncFailureIsRetryableAndNotClearedByRuntimeRefresh(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "source-sync-retry--1"
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"FIBE_DISTILLED_SOURCE_SYNC": errors.New("source sync failed"),
		},
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `{"Service":"web","State":"running","Image":"nginx:alpine"}` + "\n"},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake, InstanceID: "server-1"}}
	pg := createSourceSyncPlayground(ctx, t, st, "source-sync-retry", domain.StatusRunning, project)

	if err := w.reconcileCurrentPlayground(ctx, pg, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile playground: %v", err)
	}
	persisted, err := st.GetPlayground(ctx, fmt.Sprintf("%d", pg.ID))
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusError || persisted.StateReason == nil || *persisted.StateReason != "source_sync_failed" {
		t.Fatalf("source-sync failure should remain visible after reconcile, got %#v", persisted)
	}
	seen := strings.Join(fake.Seen, "\n")
	if strings.Contains(seen, "ps --all --format json") {
		t.Fatalf("source-sync failure should not be cleared by runtime refresh:\n%s", seen)
	}

	fake.Seen = nil
	if err := w.reconcileCurrentPlayground(ctx, persisted, time.Now().UTC()); err != nil {
		t.Fatalf("retry reconcile playground: %v", err)
	}
	if !strings.Contains(strings.Join(fake.Seen, "\n"), "FIBE_DISTILLED_SOURCE_SYNC") {
		t.Fatalf("source-sync error state should remain retryable, saw:\n%s", strings.Join(fake.Seen, "\n"))
	}
}

func TestSourceSyncSupersessionSkipsRemoteCommand(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	pg := createSourceSyncPlayground(ctx, t, st, "source-sync-stopped", domain.StatusRunning, "source-sync-stopped--1")
	stale := pg
	pg.Status = domain.StatusStopped
	if _, err := st.SavePlayground(ctx, pg); err != nil {
		t.Fatalf("save stopped playground: %v", err)
	}

	if err := w.syncPlaygroundSources(ctx, stale); err != nil {
		t.Fatalf("sync playground sources: %v", err)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("superseded source sync should not run remote commands:\n%s", strings.Join(fake.Seen, "\n"))
	}
}

func TestSourceSyncDoesNotClearSupersededLifecycleStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "source-sync-superseded--1"
	exec := &stopDuringSourceSyncExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "source-sync-superseded-spec",
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
	reason := "source_sync_dirty_work"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "source-sync-superseded-pg",
		Status:         domain.StatusHasChanges,
		StateReason:    &reason,
		PlayspecID:     ps.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec.store = st
	exec.playgroundID = pg.ID

	if err := w.syncPlaygroundSources(ctx, pg); err != nil {
		t.Fatalf("sync playground sources: %v", err)
	}
	persisted, err := st.GetPlayground(ctx, "source-sync-superseded-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("source sync must not clear superseding stopped status, got %#v", persisted)
	}
}

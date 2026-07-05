package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

func TestReconcileOncePollsSourceSyncAndPreservesDirtyWork(t *testing.T) {
	fixture := newDirtySourceSyncReconcileFixture(t)

	if err := fixture.worker.ReconcileOnce(fixture.ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	assertDirtySourceSyncPreservedWork(t, fixture)
}

func TestReconcileOnceExpiresCleanPlaygroundAndPreservesDirtySources(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	expired := time.Now().UTC().Add(-time.Hour)
	projectClean := "clean--1"
	projectDirty := "dirty--1"
	cleanPath := optfibe.PlaygroundPath(projectClean) + "/props/acme-demo/main"
	dirtyPath := optfibe.PlaygroundPath(projectDirty) + "/props/acme-demo/main"
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			cleanPath: {},
			dirtyPath: {Stdout: " M app.go\n"},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "expire-spec",
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
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	_, err = st.CreatePlayground(ctx, domain.Playground{Name: "clean", Status: domain.StatusRunning, PlayspecID: ps.ID, MarqueeID: &mq.ID, ComposeProject: &projectClean, ExpiresAt: &expired})
	if err != nil {
		t.Fatalf("create clean playground: %v", err)
	}
	_, err = st.CreatePlayground(ctx, domain.Playground{Name: "dirty", Status: domain.StatusRunning, PlayspecID: ps.ID, MarqueeID: &mq.ID, ComposeProject: &projectDirty, ExpiresAt: &expired})
	if err != nil {
		t.Fatalf("create dirty playground: %v", err)
	}

	if err := w.ReconcileOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := st.GetPlayground(ctx, "clean"); err == nil {
		t.Fatalf("expected clean playground to be deleted after expiration")
	}
	dirty, _ := st.GetPlayground(ctx, "dirty")
	if dirty.Status != domain.StatusHasChanges {
		t.Fatalf("expected dirty playground has_changes, got %#v", dirty)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "FIBE_DISTILLED_SOURCE_DIRTY") {
		t.Fatalf("expected source dirty check before expiration cleanup:\n%s", seen)
	}
	if !strings.Contains(seen, "docker compose -f compose.yml -p 'clean--1' down --remove-orphans -v") {
		t.Fatalf("expected clean compose destroy:\n%s", seen)
	}
}

func TestReconcileOnceDoesNotExpireLegacyCompletedPlayground(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	expired := time.Now().UTC().Add(-time.Hour)
	project := "legacy-completed--1"
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "legacy-completed-local", Host: "127.0.0.1", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	_, err = st.CreatePlayground(ctx, domain.Playground{
		Name:           "legacy-completed",
		Status:         domain.StatusCompleted,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		ExpiresAt:      &expired,
	})
	if err != nil {
		t.Fatalf("create completed playground: %v", err)
	}
	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	if err := w.ReconcileOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	persisted, err := st.GetPlayground(ctx, "legacy-completed")
	if err != nil {
		t.Fatalf("completed playground should remain for diagnostics: %v", err)
	}
	if persisted.Status != domain.StatusCompleted {
		t.Fatalf("completed playground status changed: %#v", persisted)
	}
	if seen := strings.Join(fake.Seen, "\n"); strings.Contains(seen, "down --remove-orphans -v") {
		t.Fatalf("completed playground must not be destroyed by expiration:\n%s", seen)
	}
}

func TestExpirationSkipsDestroyWhenExpirationIsSupersededDuringDirtyCheck(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	expired := time.Now().UTC().Add(-time.Hour)
	project := "expiration-extended--1"
	pg := createExpiringSourcePlayground(ctx, t, st, "expiration-extended", project, expired)
	exec := &mutateDuringExpirationDirtyCheckExecutor{
		store:        st,
		playgroundID: pg.ID,
		mutate:       extendPlaygroundExpiration,
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}

	if err := w.ReconcileOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	persisted, err := st.GetPlayground(ctx, "expiration-extended")
	if err != nil {
		t.Fatalf("expiration extension must preserve playground row: %v", err)
	}
	if persisted.ExpiresAt == nil || !persisted.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("expected future expiration to win, got %#v", persisted.ExpiresAt)
	}
	if seen := strings.Join(exec.Seen, "\n"); strings.Contains(seen, "down --remove-orphans -v") {
		t.Fatalf("superseded expiration must not destroy local compose:\n%s", seen)
	}
}

func TestExpirationDirtySaveDoesNotOverwriteSupersededLifecycleStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	expired := time.Now().UTC().Add(-time.Hour)
	pg := createExpiringSourcePlayground(ctx, t, st, "expiration-stopped", "expiration-stopped--1", expired)
	exec := &mutateDuringExpirationDirtyCheckExecutor{
		store:        st,
		playgroundID: pg.ID,
		stdout:       " M app.go\n",
		mutate: func(ctx context.Context, st *store.DB, id int64) error {
			return markPlaygroundStopped(ctx, st, id)
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}

	if err := w.ReconcileOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	persisted, err := st.GetPlayground(ctx, "expiration-stopped")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("dirty expiration save must not overwrite stopped status, got %#v", persisted)
	}
	if persisted.StateReason != nil {
		t.Fatalf("dirty expiration save must not write stale state reason, got %#v", persisted.StateReason)
	}
}

func TestExpirationMarksStoppedWhenSupersededAfterDestroyRuns(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	expired := time.Now().UTC().Add(-time.Hour)
	pg := createExpiringSourcePlayground(ctx, t, st, "expiration-destroyed", "expiration-destroyed--1", expired)
	exec := &mutateDuringExpirationDirtyCheckExecutor{
		store:        st,
		playgroundID: pg.ID,
		mutateOn:     "down --remove-orphans -v",
		mutate:       extendPlaygroundExpiration,
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}

	if err := w.ReconcileOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	persisted, err := st.GetPlayground(ctx, "expiration-destroyed")
	if err != nil {
		t.Fatalf("playground row should be preserved after superseded destroy: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("remote-destroyed superseded expiration must become stopped, got %#v", persisted)
	}
	if persisted.ExpiresAt == nil || !persisted.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("expected current extended expiration to be preserved, got %#v", persisted.ExpiresAt)
	}
}

func TestExpirationFailsClosedWhenPlayspecCannotBeLoaded(t *testing.T) {
	expired := time.Now().UTC().Add(-time.Hour)
	project := "missing-playspec-expire--1"
	fixture := newMissingPlayspecFixture(t, "missing-playspec-expire", project, &expired, &runtimetest.FakeExecutor{})
	corruptFixturePlayspecID(t, fixture, "missing-playspec-expire")

	if err := fixture.worker.ReconcileOnce(fixture.ctx, time.Now().UTC()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected missing playspec to stop expiration, got %v", err)
	}
	latest, err := fixture.store.GetPlayground(fixture.ctx, "missing-playspec-expire")
	if err != nil {
		t.Fatalf("playground should not be deleted: %v", err)
	}
	if latest.Status != domain.StatusRunning {
		t.Fatalf("expiration should leave status unchanged when dependency is missing, got %#v", latest)
	}
	seen := strings.Join(fixture.fake.Seen, "\n")
	if strings.Contains(seen, "down --remove-orphans") {
		t.Fatalf("missing playspec must not proceed to destructive cleanup:\n%s", seen)
	}
}

func TestExpirationFailsClosedWhenPlayspecComposeIsInvalid(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	expired := time.Now().UTC().Add(-time.Hour)
	project := "invalid-compose-expire--1"
	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "invalid-compose-expire-spec",
		BaseComposeYAML: "services:\n  web:\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "invalid-compose-expire-marquee", Host: "127.0.0.1", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	_, err = st.CreatePlayground(ctx, domain.Playground{
		Name:           "invalid-compose-expire",
		Status:         domain.StatusRunning,
		PlayspecID:     ps.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		ExpiresAt:      &expired,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	err = w.ReconcileOnce(ctx, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "validate compose for source paths") {
		t.Fatalf("expected invalid compose to stop expiration, got %v", err)
	}
	latest, err := st.GetPlayground(ctx, "invalid-compose-expire")
	if err != nil {
		t.Fatalf("playground should not be deleted: %v", err)
	}
	if latest.Status != domain.StatusRunning {
		t.Fatalf("expiration should leave status unchanged when compose is invalid, got %#v", latest)
	}
	if seen := strings.Join(fake.Seen, "\n"); strings.Contains(seen, "down --remove-orphans") {
		t.Fatalf("invalid compose must not proceed to destructive cleanup:\n%s", seen)
	}
}

func TestReconcileSkipsInProgressPlaygrounds(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"ps --all --format json": errors.New("playguard must not refresh active deploy"),
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "in-progress-marquee", Host: "127.0.0.1", User: "root", Port: 22, Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	project := "in-progress--1"
	if _, err := st.CreatePlayground(ctx, domain.Playground{Name: "in-progress-pg", Status: domain.StatusInProgress, MarqueeID: &mq.ID, ComposeProject: &project}); err != nil {
		t.Fatalf("create playground: %v", err)
	}

	if err := w.ReconcileOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile should skip in-progress playgrounds: %v", err)
	}
	for _, command := range fake.Seen {
		if strings.Contains(command, "ps --all --format json") {
			t.Fatalf("playguard refreshed in-progress playground:\n%s", strings.Join(fake.Seen, "\n"))
		}
	}
}

func TestReconcileCurrentPlaygroundSkipsDeletedRows(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "deleted-before-reconcile--1"
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "deleted-before-reconcile-marquee", Host: "127.0.0.1", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "deleted-before-reconcile-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	if err := st.DeletePlayground(ctx, fmt.Sprintf("%d", pg.ID)); err != nil {
		t.Fatalf("delete playground: %v", err)
	}

	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	if err := w.reconcileCurrentPlayground(ctx, pg, time.Now().UTC()); err != nil {
		t.Fatalf("deleted row should be skipped, got %v", err)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("deleted row should not reach runtime, saw %#v", fake.Seen)
	}
}

func TestReconcileOnceReapsRemoteOrphan(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "orphan-marquee", Host: "127.0.0.1", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	project := "owned-orphan--1"
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			optfibe.PlaygroundPath(project) + "/compose.yml": "services:\n  web:\n    image: nginx\n",
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	if err := w.ReconcileOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := fake.ReadFiles[optfibe.PlaygroundPath(project)+"/compose.yml"]; ok {
		t.Fatalf("expected orphan files to be removed")
	}
	seen := strings.Join(fake.Seen, "\n")
	for _, want := range []string{
		"docker:cleanup:owned-orphan--1:volumes=true",
		"remove:/opt/fibe/playgrounds/owned-orphan--1",
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected %q in orphan cleanup:\n%s", want, seen)
		}
	}
}

func TestReconcileOnceKeepsCurrentRemoteProject(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "current-marquee", Host: "127.0.0.1", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	project := "current-owned--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "current-owned", MarqueeID: &mq.ID, ComposeProject: &project})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	_ = pg
	fake := &runtimetest.FakeExecutor{
		ReadFiles: map[string]string{
			optfibe.PlaygroundPath(project) + "/compose.yml": "services:\n  web:\n    image: nginx\n",
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	if err := w.ReconcileOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := fake.ReadFiles[optfibe.PlaygroundPath(project)+"/compose.yml"]; !ok {
		t.Fatalf("current project files should be preserved")
	}
	seen := strings.Join(fake.Seen, "\n")
	if strings.Contains(seen, "docker:cleanup:current-owned--1") || strings.Contains(seen, "remove:/opt/fibe/playgrounds/current-owned--1") {
		t.Fatalf("current project should not be reaped:\n%s", seen)
	}
}

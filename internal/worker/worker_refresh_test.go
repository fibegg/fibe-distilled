package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
	"github.com/fibegg/fibe-distilled/internal/playguard"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

func TestRefreshRepairsMissingRuntimeArtifactsForRunningPlayground(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	project := "repair--1"
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"ps --all --format json": errors.New("open /opt/fibe/playgrounds/repair--1/compose.yml: no such file or directory"),
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	pg := createRefreshDriftFixture(ctx, t, st, "repair-spec", "repair-pg", project, "services:\n  web:\n    image: nginx:alpine\n", "nginx:alpine")

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusRunning {
		t.Fatalf("expected repaired playground to remain running, got %#v", status)
	}
	updated, err := st.GetPlayground(ctx, "repair-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if updated.Status != domain.StatusRunning || updated.ErrorMessage != nil {
		t.Fatalf("expected clean running repair, got %#v", updated)
	}
	assertSeenContainsAll(t, fake.Seen, []string{
		"write:/opt/fibe/playgrounds/repair--1/compose.yml:",
		"write:/opt/fibe/playgrounds/repair--1/docker-config/config.json:",
		"docker compose -f compose.yml -p 'repair--1' up -d --remove-orphans --pull missing",
	})
}

func TestRuntimeArtifactsMissingAcceptsTypedRemoteFileError(t *testing.T) {
	if !runtimeArtifactsMissing(fmt.Errorf("inspect compose: %w", runtime.ErrRemoteFileMissing)) {
		t.Fatalf("typed missing remote file error should classify as missing runtime artifacts")
	}
	if runtimeArtifactsMissing(errors.New("remote file missing")) {
		t.Fatalf("plain text errors should not impersonate typed remote file absence")
	}
}

func TestRefreshRepairStopsRemoteComposeWhenSupersededAfterComposeUp(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "repair-superseded--1"
	exec := &stopDuringRefreshRepairExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{Name: "repair-superseded-spec", BaseComposeYAML: "services:\n  web:\n    image: nginx:alpine\n"})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:                 "repair-superseded-pg",
		Status:               domain.StatusRunning,
		PlayspecID:           ps.ID,
		MarqueeID:            &mq.ID,
		ComposeProject:       &project,
		GeneratedComposeYAML: ps.BaseComposeYAML,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec.store = st
	exec.playgroundID = pg.ID

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusStopped {
		t.Fatalf("refresh should return superseding stopped status, got %#v", status)
	}
	seen := strings.Join(exec.Seen, "\n")
	if !strings.Contains(seen, "docker compose -f compose.yml -p 'repair-superseded--1' stop") {
		t.Fatalf("superseded repair should stop local compose, saw:\n%s", seen)
	}
}

func TestRefreshFailsWhenObservedRuntimeStateCannotBeSaved(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "stale-refresh--1"
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
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "stale-refresh-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	if err := st.DeletePlayground(ctx, "stale-refresh-pg"); err != nil {
		t.Fatalf("delete playground: %v", err)
	}

	if _, err := w.RefreshPlayground(ctx, pg); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected stale refresh save to fail with ErrNotFound, got %v", err)
	}
}

func TestRefreshClearsStaleErrorWhenRuntimeIsRunning(t *testing.T) {
	fixture := newRunningRefreshWithStaleErrorFixture(t)

	status := mustRefreshPlayground(t, fixture.worker, fixture.ctx, fixture.playground)
	assertRunningRefreshClearedStaleError(t, fixture, status)
}

func TestRefreshDoesNotOverwriteSupersededLifecycleStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "superseded-refresh--1"
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "superseded-refresh-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec := &stopDuringDeployExecutor{store: st, playgroundID: pg.ID, mutateOn: "ps --all --format json"}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusStopped {
		t.Fatalf("refresh should return the superseding stopped status, got %#v", status)
	}
	persisted, err := st.GetPlayground(ctx, "superseded-refresh-pg")
	if err != nil {
		t.Fatalf("get persisted playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("refresh must not overwrite stopped with stale running observation, got %#v", persisted)
	}
}

func TestRefreshDoesNotPromoteRunningUntilNoHealthcheckRouteIsReady(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_INSPECT": {Stdout: `[{"Service":"web","Image":"nginx:alpine","State":"running","Health":"","ExitCode":0}]`},
		},
	}
	probe := &sequenceRouteProbeClient{statuses: []int{http.StatusBadGateway, http.StatusOK}}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}, RouteProbeClient: probe}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "route-refresh-marquee", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	project := "route-refresh--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "route-refresh-pg",
		Status:         domain.StatusStopped,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		Services:       []domain.PlaygroundServiceInfo{{Name: "web", Image: "nginx:alpine", Status: "stopped"}},
		ServiceURLs:    []domain.PlaygroundServiceURL{{Name: "web", URL: "https://route-refresh.example.test"}},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh first pass: %v", err)
	}
	if status.Status != domain.StatusStopped {
		t.Fatalf("502 route should not promote stopped row to running, got %#v", status)
	}
	persisted, err := st.GetPlayground(ctx, "route-refresh-pg")
	if err != nil {
		t.Fatalf("get persisted playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("expected persisted stopped status before route recovery, got %#v", persisted)
	}

	status, err = w.RefreshPlayground(ctx, persisted)
	if err != nil {
		t.Fatalf("refresh recovered route: %v", err)
	}
	if status.Status != domain.StatusRunning {
		t.Fatalf("healthy route should promote running service, got %#v", status)
	}
	if probe.calls < 2 {
		t.Fatalf("expected refresh to probe route on both passes, calls=%d", probe.calls)
	}
}

func TestRefreshMarksRunningPlaygroundStoppedWhenComposeHasNoServices(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_INSPECT": {Stdout: `[]`},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "empty-compose-marquee", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	project := "empty-compose--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "empty-compose-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		Services:       []domain.PlaygroundServiceInfo{{Name: "web", Image: "nginx:alpine", Status: "running", Running: true}},
		ServiceURLs:    []domain.PlaygroundServiceURL{{Name: "web", URL: "https://web.example.test"}},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusStopped {
		t.Fatalf("empty compose service list should stop running playground, got %#v", status)
	}
	persisted, err := st.GetPlayground(ctx, "empty-compose-pg")
	if err != nil {
		t.Fatalf("get persisted playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("expected persisted stopped status, got %#v", persisted)
	}
	assertEmptyObservationStoppedService(t, persisted)
	assertStoppedServiceURL(t, persisted.ServiceURLs, "web")

	urlOnlyProject := "empty-compose-url-only--1"
	running := true
	urlOnly, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "empty-compose-url-only-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &urlOnlyProject,
		ServiceURLs: []domain.PlaygroundServiceURL{{
			Name:    "api",
			URL:     "https://api.example.test",
			Status:  domain.StatusRunning,
			Running: &running,
		}},
	})
	if err != nil {
		t.Fatalf("create url-only playground: %v", err)
	}
	if _, err := w.RefreshPlayground(ctx, urlOnly); err != nil {
		t.Fatalf("refresh url-only playground: %v", err)
	}
	urlOnlyPersisted, err := st.GetPlayground(ctx, "empty-compose-url-only-pg")
	if err != nil {
		t.Fatalf("get url-only playground: %v", err)
	}
	assertStoppedServiceURL(t, urlOnlyPersisted.ServiceURLs, "api")
}

func TestRefreshClaimsCurrentRowBeforeInspect(t *testing.T) {
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
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "refresh-current-marquee", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	oldProject := "refresh-current-old--1"
	newProject := "refresh-current-new--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "refresh-current-pg",
		Status:         domain.StatusRunning,
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

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusRunning {
		t.Fatalf("expected running status, got %#v", status)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "base='/opt/fibe/playgrounds/refresh-current-new--1'") || !strings.Contains(seen, "project='refresh-current-new--1'") {
		t.Fatalf("refresh should inspect current compose project, saw:\n%s", seen)
	}
	if strings.Contains(seen, "refresh-current-old--1") {
		t.Fatalf("refresh should not inspect stale compose project, saw:\n%s", seen)
	}
}

func TestRefreshSupersessionSkipsRemoteInspect(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "refresh-skip-marquee", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	project := "refresh-skip--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "refresh-skip-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	current := pg
	current.Status = domain.StatusStopped
	if _, err := st.SavePlayground(ctx, current); err != nil {
		t.Fatalf("save superseding status: %v", err)
	}

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusStopped {
		t.Fatalf("refresh should return superseding stopped status, got %#v", status)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("superseded refresh should not inspect remote runtime, saw:\n%s", strings.Join(fake.Seen, "\n"))
	}
}

func TestRefreshFailsWhenRuntimeMarqueeCannotBeLoaded(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "missing-marquee--1"
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "missing-marquee-pg",
		Status:         domain.StatusRunning,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	if _, err := rawDB.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatalf("disable raw foreign keys: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `DELETE FROM marquees WHERE id=?`, mq.ID); err != nil {
		t.Fatalf("remove marquee fixture row: %v", err)
	}

	if _, err := (Worker{DB: st}).RefreshPlayground(ctx, pg); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected missing marquee to fail refresh, got %v", err)
	}
}

func TestRefreshFailsWhenRuntimeArtifactDriftCheckFails(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "artifact-check-fail--1"
	base := optfibe.PlaygroundPath(project)
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","Image":"nginx:alpine","State":"running","Health":"healthy","ExitCode":0}]`},
		},
		ReadFiles: map[string]string{
			base + "/compose.yml": "services:\n  web:\n    image: nginx:alpine\n",
		},
		ReadErrors: map[string]error{
			base + "/docker-config/config.json": errors.New("docker config unreadable"),
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:                 "artifact-check-fail-pg",
		Status:               domain.StatusRunning,
		MarqueeID:            &mq.ID,
		ComposeProject:       &project,
		GeneratedComposeYAML: "services:\n  web:\n    image: nginx:alpine\n",
		Services:             []domain.PlaygroundServiceInfo{{Name: "web", Status: "running", Image: "nginx:alpine", Running: true}},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	if _, err := w.RefreshPlayground(ctx, pg); err == nil || !strings.Contains(err.Error(), "runtime artifact drift check failed") || !strings.Contains(err.Error(), "docker config unreadable") {
		t.Fatalf("expected artifact drift check error, got %v", err)
	}
}

func TestRefreshSkipsRuntimeRepairWhenSourceWorkIsDirty(t *testing.T) {
	fixture := newDirtyRepairRefreshFixture(t)

	status := mustRefreshPlayground(t, fixture.worker, fixture.ctx, fixture.playground)
	assertDirtyRepairSkippedRedeploy(t, fixture, status)
}

func TestRuntimeRepairFailsClosedWhenPlayspecCannotBeLoaded(t *testing.T) {
	project := "repair-missing-playspec--1"
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"ps --all --format json": errors.New("open /opt/fibe/playgrounds/repair-missing-playspec--1/compose.yml: no such file or directory"),
		},
	}
	fixture := newMissingPlayspecFixture(t, "repair-missing-playspec-pg", project, nil, fake)
	corrupt := corruptFixturePlayspecID(t, fixture, "repair-missing-playspec-pg")

	if _, err := fixture.worker.RefreshPlayground(fixture.ctx, corrupt); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected missing playspec to stop repair, got %v", err)
	}
	seen := strings.Join(fixture.fake.Seen, "\n")
	if strings.Contains(seen, " up -d --remove-orphans --pull missing") || strings.Contains(seen, "FIBE_DISTILLED_SOURCE_SYNC") {
		t.Fatalf("missing playspec must not sync or redeploy repair:\n%s", seen)
	}
}

func TestRefreshRepairsRuntimeArtifactDriftAfterSuccessfulInspection(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "artifact-drift--1"
	base := optfibe.PlaygroundPath(project)
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","Image":"nginx:alpine","State":"running","Health":"healthy","ExitCode":0}]`},
		},
		ReadFiles: map[string]string{
			base + "/compose.yml": "services:\n  web:\n    image: httpd\n",
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	pg := createRefreshDriftFixture(ctx, t, st, "artifact-drift-spec", "artifact-drift-pg", project, "services:\n  web:\n    image: nginx:alpine\n", "nginx:alpine")

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusRunning {
		t.Fatalf("expected repaired running status, got %#v", status)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "read:"+base+"/compose.yml") {
		t.Fatalf("expected artifact drift check:\n%s", seen)
	}
	if !strings.Contains(seen, "docker compose -f compose.yml -p 'artifact-drift--1' up -d --remove-orphans --pull missing") {
		t.Fatalf("expected drift repair compose up:\n%s", seen)
	}
}

func TestRefreshRepairsImageDrift(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "image-drift--1"
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","Image":"nginx:old","State":"running","Health":"healthy","ExitCode":0}]`},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	pg := createRefreshDriftFixture(ctx, t, st, "image-drift-spec", "image-drift-pg", project, "services:\n  web:\n    image: nginx:new\n", "nginx:new")

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusRunning {
		t.Fatalf("expected repaired running status, got %#v", status)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "docker compose -f compose.yml -p 'image-drift--1' up -d --remove-orphans --pull missing") {
		t.Fatalf("expected image drift repair compose up:\n%s", seen)
	}
}

func TestImageDriftDetectedTrimsServiceNames(t *testing.T) {
	expected := []domain.PlaygroundServiceInfo{{Name: " web ", Image: "nginx:new"}}
	observed := []domain.PlaygroundServiceInfo{{Name: "web", Image: "nginx:old"}}
	if !playguard.ImageDriftDetected(expected, observed) {
		t.Fatal("expected image drift to use normalized service names")
	}
}

func TestMergeServiceImagesTrimsServiceNames(t *testing.T) {
	existing := []domain.PlaygroundServiceInfo{{Name: " web ", Image: "nginx:rendered"}}
	inspected := []domain.PlaygroundServiceInfo{{Name: "web"}}
	merged := mergeServiceImages(existing, inspected)
	if len(merged) != 1 || merged[0].Image != "nginx:rendered" {
		t.Fatalf("expected rendered image to be preserved by normalized service name, got %#v", merged)
	}
}

func TestRefreshSkipsImageDriftRepairDuringCooldown(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "image-drift-cooldown--1"
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","Image":"nginx:old","State":"running","Health":"healthy","ExitCode":0}]`},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{Name: "image-drift-cooldown-spec", BaseComposeYAML: "services:\n  web:\n    image: nginx:new\n"})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	lockUntil := time.Now().UTC().Add(defaultRuntimeRepairCooldown)
	reason := "image_drift"
	needsRecreation := true
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:                     "image-drift-cooldown-pg",
		Status:                   domain.StatusRunning,
		PlayspecID:               ps.ID,
		MarqueeID:                &mq.ID,
		ComposeProject:           &project,
		GeneratedComposeYAML:     ps.BaseComposeYAML,
		Services:                 []domain.PlaygroundServiceInfo{{Name: "web", Status: "running", Image: "nginx:new", Running: true}},
		PlayguardRepairReason:    &reason,
		PlayguardRepairLockUntil: &lockUntil,
		NeedsRecreation:          &needsRecreation,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	status, err := w.RefreshPlayground(ctx, pg)
	if err != nil {
		t.Fatalf("refresh playground: %v", err)
	}
	if status.Status != domain.StatusRunning {
		t.Fatalf("expected running status during cooldown, got %#v", status)
	}
	if !status.NeedsRecreation {
		t.Fatalf("expected status to expose pending recreation during cooldown: %#v", status)
	}
	seen := strings.Join(fake.Seen, "\n")
	if strings.Contains(seen, " up -d --remove-orphans --pull missing") {
		t.Fatalf("cooldown should suppress redeploy:\n%s", seen)
	}
	if strings.Contains(seen, "read:"+optfibe.PlaygroundPath(project)+"/docker-config/config.json") {
		t.Fatalf("cooldown should return before artifact drift repair loop:\n%s", seen)
	}
}

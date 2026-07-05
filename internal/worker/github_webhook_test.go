package worker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/buildrecord"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

func TestHandleGitHubPushSyncsMatchingSourceMountedService(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	mq := mustWebhookRuntimeMarquee(t, ctx, st)
	ps := mustWebhookPlayspec(t, ctx, st, "webhook-source-spec", `services:
  api:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /api
      fibe.gg/branch: main
  web:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
      fibe.gg/branch: main
`)
	project := "webhook-source--1"
	mustWebhookPlayground(t, ctx, st, webhookPlaygroundInput{
		name:           "webhook-source-pg",
		status:         domain.StatusHasChanges,
		playspecID:     ps.ID,
		marqueeID:      mq.ID,
		composeProject: project,
		serviceBranches: map[string]any{
			"web": map[string]any{"git_config": map[string]any{"branch_name": "feature/new-ui"}},
		},
	})
	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	result, err := w.HandleGitHubPush(ctx, GitHubPushEvent{
		RepositoryFullName: "acme/demo",
		Branch:             "feature/new-ui",
		After:              "abcdef1234567890",
	})
	if err != nil {
		t.Fatalf("handle push: %v", err)
	}
	if result.MatchedPlaygrounds != 1 || result.SyncedSources != 1 || result.BuiltServices != 0 {
		t.Fatalf("unexpected push result: %#v", result)
	}
	seen := strings.Join(fake.Seen, "\n")
	if !strings.Contains(seen, "/opt/fibe/playgrounds/webhook-source--1/props/acme-demo/feature-new-ui") {
		t.Fatalf("expected effective branch source sync:\n%s", seen)
	}
	if strings.Contains(seen, "/opt/fibe/playgrounds/webhook-source--1/props/acme-demo/main") {
		t.Fatalf("webhook should not sync unmatched service branch:\n%s", seen)
	}
}

func TestHandleGitHubPushIgnoresUnmatchedPlaygrounds(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	mq := mustWebhookRuntimeMarquee(t, ctx, st)
	ps := mustWebhookPlayspec(t, ctx, st, "webhook-unmatched-spec", `services:
  web:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
      fibe.gg/branch: main
`)
	project := "webhook-unmatched--1"
	mustWebhookPlayground(t, ctx, st, webhookPlaygroundInput{
		name:           "webhook-unmatched-pg",
		status:         domain.StatusRunning,
		playspecID:     ps.ID,
		marqueeID:      mq.ID,
		composeProject: project,
	})
	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	result, err := w.HandleGitHubPush(ctx, GitHubPushEvent{
		RepositoryFullName: "acme/demo",
		Branch:             "feature/other",
		After:              "abcdef1234567890",
	})
	if err != nil {
		t.Fatalf("handle push: %v", err)
	}
	if result != (GitHubPushResult{}) {
		t.Fatalf("unmatched push should be a no-op, got %#v", result)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("unmatched push should not run remote commands:\n%s", strings.Join(fake.Seen, "\n"))
	}
}

func TestHandleGitHubPushPreservesDirtySourceWork(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	mq := mustWebhookRuntimeMarquee(t, ctx, st)
	ps := mustWebhookPlayspec(t, ctx, st, "webhook-dirty-spec", `services:
  web:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
`)
	project := "webhook-dirty--1"
	mustWebhookPlayground(t, ctx, st, webhookPlaygroundInput{
		name:           "webhook-dirty-pg",
		status:         domain.StatusRunning,
		playspecID:     ps.ID,
		marqueeID:      mq.ID,
		composeProject: project,
	})
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{"FIBE_DISTILLED_SOURCE_SYNC": errors.New("dirty checkout")},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_SOURCE_SYNC": {Stderr: "fibe_distilled_source_sync_category=dirty_work\nfibe_distilled_source_sync_dirty_entries=1\n"},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	result, err := w.HandleGitHubPush(ctx, GitHubPushEvent{
		RepositoryFullName: "acme/demo",
		Branch:             "main",
		After:              "abcdef1234567890",
	})
	if err == nil {
		t.Fatal("dirty source sync should return the recorded source-sync error")
	}
	if result.MatchedPlaygrounds != 1 || result.SyncedSources != 1 {
		t.Fatalf("unexpected dirty push result: %#v", result)
	}
	updated, err := st.GetPlayground(ctx, "webhook-dirty-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if updated.Status != domain.StatusHasChanges || updated.StateReason == nil || *updated.StateReason != "source_sync_dirty_work" {
		t.Fatalf("dirty source sync should preserve work as has_changes, got %#v", updated)
	}
}

func TestHandleGitHubPushSyncsSourceMountedBuildService(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	mq := mustWebhookRuntimeMarquee(t, ctx, st)
	ps := mustWebhookPlayspec(t, ctx, st, "webhook-source-build-spec", `services:
  web:
    image: node:22
    build:
      context: .
      dockerfile: Dockerfile
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
      fibe.gg/production: "false"
`)
	project := "webhook-source-build--1"
	mustWebhookPlayground(t, ctx, st, webhookPlaygroundInput{
		name:           "webhook-source-build-pg",
		status:         domain.StatusRunning,
		playspecID:     ps.ID,
		marqueeID:      mq.ID,
		composeProject: project,
	})
	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	result, err := w.HandleGitHubPush(ctx, GitHubPushEvent{
		RepositoryFullName: "acme/demo",
		Branch:             "main",
		After:              "abcdef1234567890",
	})
	if err != nil {
		t.Fatalf("handle push: %v", err)
	}
	if result.MatchedPlaygrounds != 1 || result.SyncedSources != 1 || result.BuiltServices != 0 || result.FailedBuilds != 0 {
		t.Fatalf("source-mounted build service should sync without BuildRecord, got %#v", result)
	}
	updated, err := st.GetPlayground(ctx, "webhook-source-build-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if updated.Status != domain.StatusRunning || updated.StateReason != nil || len(updated.BuildStatuses) != 0 {
		t.Fatalf("source-mounted build service should stay running without build-ready state, got %#v", updated)
	}
	seen := strings.Join(fake.Seen, "\n")
	if strings.Contains(seen, "FIBE_DISTILLED_BUILD_IMAGE") {
		t.Fatalf("source-mounted build service must not create a BuildRecord:\n%s", seen)
	}
	if !strings.Contains(seen, "ps --all --format json") {
		t.Fatalf("source-mounted push should refresh runtime state:\n%s", seen)
	}
}

func TestHandleGitHubPushBuildsProductionServiceWithoutRollout(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	mq := mustWebhookRuntimeMarquee(t, ctx, st)
	ps := mustWebhookPlayspec(t, ctx, st, "webhook-production-spec", `services:
  web:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
      fibe.gg/production: "true"
`)
	project := "webhook-production--1"
	pg := mustWebhookPlayground(t, ctx, st, webhookPlaygroundInput{
		name:           "webhook-production-pg",
		status:         domain.StatusRunning,
		playspecID:     ps.ID,
		marqueeID:      mq.ID,
		composeProject: project,
	})
	active := mustWebhookBuildRecord(t, ctx, st, pg.ID, "web", "main", "1111111111111111", "fibe-distilled/webhook-production/web:1111111111111111")
	pg.BuildStatuses = []domain.PlaygroundBuildStatus{buildrecord.StatusFromRecord("web", "main", active)}
	if _, err := st.SavePlayground(ctx, pg); err != nil {
		t.Fatalf("save active build status: %v", err)
	}
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_RESOLVE_COMMIT": {Stdout: "abcdef1234567890\n"},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	result, err := w.HandleGitHubPush(ctx, GitHubPushEvent{
		RepositoryFullName: "acme/demo",
		Branch:             "main",
		After:              "abcdef1234567890",
	})
	if err != nil {
		t.Fatalf("handle push: %v", err)
	}
	if result.MatchedPlaygrounds != 1 || result.SyncedSources != 1 || result.BuiltServices != 1 {
		t.Fatalf("unexpected production push result: %#v", result)
	}
	updated, err := st.GetPlayground(ctx, "webhook-production-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if updated.Status != domain.StatusHasChanges || updated.StateReason == nil || *updated.StateReason != "webhook_build_ready" {
		t.Fatalf("production webhook build should mark deployable changes, got %#v", updated)
	}
	if len(updated.BuildStatuses) != 1 || updated.BuildStatuses[0].Latest == nil || updated.BuildStatuses[0].Active == nil {
		t.Fatalf("expected active and latest build snapshots, got %#v", updated.BuildStatuses)
	}
	if updated.BuildStatuses[0].Active.ID != active.ID {
		t.Fatalf("webhook build should preserve deployed active build, got %#v", updated.BuildStatuses[0])
	}
	if updated.BuildStatuses[0].Latest.ID == active.ID || updated.BuildStatuses[0].Latest.CommitSHA != "abcdef1234567890" {
		t.Fatalf("webhook build should record new latest build, got %#v", updated.BuildStatuses[0])
	}
	if strings.Contains(updated.GeneratedComposeYAML, "abcdef1234567890") {
		t.Fatalf("webhook build must not rollout generated compose, got:\n%s", updated.GeneratedComposeYAML)
	}
}

func TestSourceSyncPlansSerializeSameCheckoutPath(t *testing.T) {
	exec := newBlockingSourceSyncExecutor()
	w := Worker{Runtime: runtime.Checker{Executor: exec}}
	ps := domain.Playspec{BaseComposeYAML: `services:
  web:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
`}
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := w.syncSources(ctx, domain.Marquee{Name: "local"}, "lock-test--1", ps); err != nil {
			t.Errorf("first sync sources: %v", err)
		}
	}()
	exec.waitForStarted(t, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := w.syncSources(ctx, domain.Marquee{Name: "local"}, "lock-test--1", ps); err != nil {
			t.Errorf("second sync sources: %v", err)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	if got := exec.startedCount(); got != 1 {
		t.Fatalf("second source sync entered remote command before first completed; started=%d", got)
	}
	exec.release()
	wg.Wait()
	if exec.maxActiveCount() != 1 || exec.startedCount() != 2 {
		t.Fatalf("source sync lock did not serialize commands: started=%d maxActive=%d", exec.startedCount(), exec.maxActiveCount())
	}
}

type webhookPlaygroundInput struct {
	name            string
	status          string
	playspecID      *int64
	marqueeID       int64
	composeProject  string
	serviceBranches map[string]any
}

func mustWebhookRuntimeMarquee(t *testing.T, ctx context.Context, st *store.DB) domain.Marquee {
	t.Helper()
	domains := "apps.example.com"
	acmeEmail := "ops@example.com"
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{
		Name:         "default",
		Host:         "127.0.0.1",
		User:         "root",
		Port:         22,
		DomainsInput: &domains,
		AcmeEmail:    &acmeEmail,
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	return mq
}

func mustWebhookPlayspec(t *testing.T, ctx context.Context, st *store.DB, name string, composeYAML string) domain.Playspec {
	t.Helper()
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{Name: name, BaseComposeYAML: composeYAML})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	return ps
}

func mustWebhookPlayground(t *testing.T, ctx context.Context, st *store.DB, input webhookPlaygroundInput) domain.Playground {
	t.Helper()
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:            input.name,
		Status:          input.status,
		PlayspecID:      input.playspecID,
		MarqueeID:       &input.marqueeID,
		ComposeProject:  &input.composeProject,
		ServiceBranches: input.serviceBranches,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	return pg
}

func mustWebhookBuildRecord(t *testing.T, ctx context.Context, st *store.DB, playgroundID int64, service string, branch string, commit string, imageRef string) domain.BuildRecord {
	t.Helper()
	record, err := st.CreateBuildRecord(ctx, domain.BuildRecord{
		PlaygroundID: &playgroundID,
		ServiceName:  service,
		Branch:       branch,
		CommitSHA:    commit,
		Status:       domain.BuildStatusSuccess,
		ImageRef:     imageRef,
	})
	if err != nil {
		t.Fatalf("create build record: %v", err)
	}
	return record
}

type blockingSourceSyncExecutor struct {
	mu        sync.Mutex
	started   int
	active    int
	maxActive int
	entered   chan struct{}
	done      chan struct{}
}

func newBlockingSourceSyncExecutor() *blockingSourceSyncExecutor {
	return &blockingSourceSyncExecutor{
		entered: make(chan struct{}, 2),
		done:    make(chan struct{}),
	}
}

func (e *blockingSourceSyncExecutor) Run(_ context.Context, _ domain.Marquee, command string) (runtime.CommandResult, error) {
	if !strings.Contains(command, "FIBE_DISTILLED_SOURCE_SYNC") {
		return runtime.CommandResult{Stdout: "ok"}, nil
	}
	e.block()
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *blockingSourceSyncExecutor) Sync(_ context.Context, _ domain.Marquee, req runtime.GitSyncRequest) error {
	_ = req
	e.block()
	return nil
}

func (e *blockingSourceSyncExecutor) DirtyPaths(context.Context, domain.Marquee, string, []string) ([]string, error) {
	return nil, nil
}

func (e *blockingSourceSyncExecutor) Head(context.Context, domain.Marquee, string, string) (string, error) {
	return "abcdef1234567890", nil
}

func (e *blockingSourceSyncExecutor) block() {
	e.mu.Lock()
	e.started++
	e.active++
	if e.active > e.maxActive {
		e.maxActive = e.active
	}
	e.mu.Unlock()
	e.entered <- struct{}{}
	<-e.done
	e.mu.Lock()
	e.active--
	e.mu.Unlock()
}

func (e *blockingSourceSyncExecutor) WriteFile(context.Context, domain.Marquee, string, string) (runtime.CommandResult, error) {
	return runtime.CommandResult{Stdout: "ok"}, nil
}

func (e *blockingSourceSyncExecutor) waitForStarted(t *testing.T, count int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for e.startedCount() < count {
		select {
		case <-e.entered:
		case <-deadline:
			t.Fatalf("timed out waiting for %d source sync commands; got %d", count, e.startedCount())
		}
	}
}

func (e *blockingSourceSyncExecutor) release() {
	close(e.done)
}

func (e *blockingSourceSyncExecutor) startedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

func (e *blockingSourceSyncExecutor) maxActiveCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxActive
}

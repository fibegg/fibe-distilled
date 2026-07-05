package worker

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/buildrecord"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

func TestFailPlaygroundPreservesRuntimeAndSaveErrors(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	w := Worker{DB: st}

	pg, err := w.failPlayground(ctx, domain.Playground{ID: 123, Name: "save-fail-pg", Status: domain.StatusRunning}, errors.New("compose deploy failed"), []string{"web"})
	if err == nil {
		t.Fatalf("expected joined runtime/save error")
	}
	if !strings.Contains(err.Error(), "compose deploy failed") || !strings.Contains(strings.ToLower(err.Error()), "closed") {
		t.Fatalf("expected runtime and save errors, got %v", err)
	}
	if pg.Status != domain.StatusError {
		t.Fatalf("returned playground should carry error status even when save fails: %#v", pg)
	}
}

func TestFailPlaygroundDoesNotOverwriteUnreadableLatestRow(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer closeStore(t, st)

	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "unreadable-latest-pg", Status: domain.StatusRunning})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer closeSQLDB(t, rawDB)
	if _, err := rawDB.ExecContext(ctx, `UPDATE playgrounds SET services_json='{' WHERE id=?`, pg.ID); err != nil {
		t.Fatalf("corrupt playground services json: %v", err)
	}

	w := Worker{DB: st}
	_, err = w.failPlayground(ctx, pg, errors.New("runtime inspect failed"), []string{"web"})
	if err == nil {
		t.Fatalf("expected unreadable latest row to fail")
	}
	if !strings.Contains(err.Error(), "runtime inspect failed") || !strings.Contains(err.Error(), "playgrounds.services_json") {
		t.Fatalf("expected runtime and latest-row decode errors, got %v", err)
	}
}

func TestDeployPlaygroundBuildsDynamicServicesAndInjectsImageRefs(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "demo--42"
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_RESOLVE_COMMIT": {Stdout: "abcdef1234567890\n"},
			"ps --all --format json":        {Stdout: `[{"Service":"web","Image":"fibe-distilled/demo--42/web:abcdef1234567890","State":"running","Health":"healthy","ExitCode":0}]`},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "build-spec",
		BaseComposeYAML: `services:
  web:
    build: .
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/dockerfile: Dockerfile.web
      fibe.gg/build_target: prod
      fibe.gg/build_args: RAILS_ENV=production
`,
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "build-pg", PlayspecID: ps.ID, ComposeProject: &project})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	buildPlatform := "linux/amd64"
	mq := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22, BuildPlatform: &buildPlatform}

	updated, err := w.DeployPlayground(ctx, pg, ps, &mq)
	if err != nil {
		t.Fatalf("deploy playground: %v", err)
	}
	assertActiveDemoBuildStatus(t, updated)
	assertComposeUsesDemoBuiltImage(t, updated.GeneratedComposeYAML)
	assertExecutorSaw(t, fake, "'--platform' 'linux/amd64'", "local build command to include build platform")
	assertPersistedDemoBuildRecord(t, ctx, st, dbPath, updated, pg.ID)
}

func TestDeployPlaygroundReusesVerifiedBuildRecord(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "reuse--42"
	repositoryURL := "https://github.com/acme/reuse-demo.git"
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "reuse-spec",
		BaseComposeYAML: `services:
  web:
    build: .
    labels:
      fibe.gg/repo_url: https://github.com/acme/reuse-demo.git
      fibe.gg/dockerfile: Dockerfile.web
      fibe.gg/build_target: prod
      fibe.gg/build_args: RAILS_ENV=production
`,
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	summaries, err := buildrecord.Services(ps)
	if err != nil {
		t.Fatalf("build summaries: %v", err)
	}
	identity := buildrecord.IdentityForService(summaries[0])
	prop, err := st.CreateProp(ctx, domain.Prop{Name: "reuse-prop", RepositoryURL: repositoryURL})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	propID := prop.ID
	reusedImage := "fibe-distilled/prior/web:abcdef1234567890"
	if _, err := st.CreateBuildRecord(ctx, domain.BuildRecord{
		PropID:              &propID,
		ServiceName:         "web",
		Branch:              "main",
		CommitSHA:           "abcdef1234567890",
		Status:              "success",
		ImageRef:            reusedImage,
		BuildDockerfilePath: identity.DockerfilePath,
		BuildTarget:         identity.BuildTarget,
		BuildArgsDigest:     identity.BuildArgsDigest,
		BuildIdentityDigest: identity.BuildIdentityDigest,
		BuildPlatform:       "linux/amd64",
		BuildCacheKey:       identity.BuildIdentityDigest,
	}); err != nil {
		t.Fatalf("create reusable build record: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "reuse-pg", PlayspecID: ps.ID, ComposeProject: &project})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	buildPlatform := "linux/amd64"
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_RESOLVE_COMMIT": {Stdout: "abcdef1234567890\n"},
			"docker image inspect '" + reusedImage + "'": {
				Stdout: `{"Labels":{"fibe.build.git_commit_sha":"abcdef1234567890","fibe.build.identity_digest":"` + identity.BuildIdentityDigest + `"}}`,
			},
			"ps --all --format json": {
				Stdout: `[{"Service":"web","Image":"` + reusedImage + `","State":"running","Health":"healthy","ExitCode":0}]`,
			},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	mq := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22, BuildPlatform: &buildPlatform}

	updated, err := w.DeployPlayground(ctx, pg, ps, &mq)
	if err != nil {
		t.Fatalf("deploy playground: %v", err)
	}
	assertReusedBuildSnapshot(t, updated, reusedImage)
	assertReusedBuildRecord(t, ctx, st, dbPath, updated, pg.ID, prop.ID, reusedImage)
	assertReusedComposeImage(t, updated.GeneratedComposeYAML, reusedImage)
	assertRemoteBuildNotSeen(t, fake)
}

func TestFindReusableBuildRecordAcceptsInFlightImageEvidence(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prop, err := st.CreateProp(ctx, domain.Prop{Name: "inflight-prop", RepositoryURL: "https://github.com/acme/inflight.git"})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	propID := prop.ID
	started := time.Now().UTC()
	candidate, err := st.CreateBuildRecord(ctx, domain.BuildRecord{
		PropID:              &propID,
		ServiceName:         "web",
		Branch:              "main",
		CommitSHA:           "abcdef123",
		Status:              "building",
		ImageRef:            "fibe-distilled/inflight/web:abcdef123",
		BuildIdentityDigest: "identity-digest",
		BuildPlatform:       "linux/amd64",
		StartedAt:           &started,
	})
	if err != nil {
		t.Fatalf("create candidate build record: %v", err)
	}
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"docker image inspect 'fibe-distilled/inflight/web:abcdef123'": {
				Stdout: `{"Labels":{"fibe.build.git_commit_sha":"abcdef123","fibe.build.identity_digest":"identity-digest"}}`,
			},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	reusable, found, err := w.findReusableBuildRecord(ctx, domain.Marquee{Name: "local"}, buildrecord.Plan{
		PlaygroundID: 99,
		PropID:       &propID,
		ServiceName:  "web",
		Branch:       "main",
		Identity:     buildrecord.Identity{BuildIdentityDigest: "identity-digest"},
		Platform:     "linux/amd64",
	}, domain.BuildRecord{
		ID:                  candidate.ID + 1,
		CommitSHA:           "abcdef123",
		BuildIdentityDigest: "identity-digest",
		BuildPlatform:       "linux/amd64",
	})
	if err != nil {
		t.Fatalf("find reusable build record: %v", err)
	}
	if !found || reusable.ID != candidate.ID {
		t.Fatalf("expected in-flight candidate reuse, got %#v", reusable)
	}
}

func TestFindReusableBuildRecordSkipsStaleBuildingCandidatesWithoutMutation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prop, err := st.CreateProp(ctx, domain.Prop{Name: "stale-inflight-prop", RepositoryURL: "https://github.com/acme/stale-inflight.git"})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	propID := prop.ID
	staleStarted := time.Now().UTC().Add(-defaultBuildStaleTimeout - time.Minute)
	candidate, err := st.CreateBuildRecord(ctx, domain.BuildRecord{
		PropID:              &propID,
		ServiceName:         "web",
		Branch:              "main",
		CommitSHA:           "abcdef123",
		Status:              domain.BuildStatusBuilding,
		ImageRef:            "fibe-distilled/stale-inflight/web:abcdef123",
		BuildIdentityDigest: "identity-digest",
		BuildPlatform:       "linux/amd64",
		Logs:                "still building",
		StartedAt:           &staleStarted,
	})
	if err != nil {
		t.Fatalf("create stale candidate build record: %v", err)
	}
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"docker image inspect": errors.New("stale building record must not be inspected"),
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}

	reusable, found, err := w.findReusableBuildRecord(ctx, domain.Marquee{Name: "local"}, buildrecord.Plan{
		PlaygroundID: 99,
		PropID:       &propID,
		ServiceName:  "web",
		Branch:       "main",
		Identity:     buildrecord.Identity{BuildIdentityDigest: "identity-digest"},
		Platform:     "linux/amd64",
	}, domain.BuildRecord{
		ID:                  candidate.ID + 1,
		CommitSHA:           "abcdef123",
		BuildIdentityDigest: "identity-digest",
		BuildPlatform:       "linux/amd64",
	})
	if err != nil {
		t.Fatalf("find reusable build record: %v", err)
	}
	if found || reusable.ID != 0 {
		t.Fatalf("stale building candidate should not be reused, found=%v reusable=%#v", found, reusable)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("stale building candidate must not trigger runtime inspection:\n%s", strings.Join(fake.Seen, "\n"))
	}
	preserved, err := st.GetBuildRecord(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("get preserved candidate: %v", err)
	}
	if preserved.Status != domain.BuildStatusBuilding || preserved.CompletedAt != nil || preserved.ErrorMessage != nil || preserved.Logs != "still building" {
		t.Fatalf("stale building candidate should remain unchanged, got %#v", preserved)
	}
	if preserved.StartedAt == nil || !preserved.StartedAt.Equal(staleStarted) {
		t.Fatalf("stale building candidate started_at changed, got %#v want %s", preserved.StartedAt, staleStarted)
	}
}

func TestBuildServicesFailsClosedOnInvalidCompose(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "invalid-build-services"})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	ps := domain.Playspec{BaseComposeYAML: "services:\n  web:\n"}

	stale := domain.PlaygroundBuildStatus{ServiceName: "old", Latest: &domain.PlaygroundBuildRecordSnapshot{Status: domain.BuildStatusSuccess}}
	pg.BuildStatuses = []domain.PlaygroundBuildStatus{stale}
	builds, err := w.buildServices(ctx, pg, ps, domain.Marquee{Name: "local"})
	if err == nil || !strings.Contains(err.Error(), "validate compose for builds") {
		t.Fatalf("expected build-services validation error, got %v", err)
	}
	if len(builds.statuses) != 0 || len(builds.imageRefs) != 0 {
		t.Fatalf("preflight build error must not return stale build outputs, got statuses=%#v images=%#v", builds.statuses, builds.imageRefs)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("invalid compose must not run build commands:\n%s", strings.Join(fake.Seen, "\n"))
	}
}

func TestBuildServicesRequiresComposeProject(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	pg := domain.Playground{ID: 43, Name: "unpersisted"}
	ps := domain.Playspec{BaseComposeYAML: `services:
  web:
    build: .
    labels:
      fibe.gg/repo_url: https://github.com/acme/missing-project.git
`}

	builds, err := w.buildServices(ctx, pg, ps, domain.Marquee{Name: "local"})
	if err == nil || !strings.Contains(err.Error(), "compose project is required") {
		t.Fatalf("expected compose project error, got %v", err)
	}
	if len(builds.statuses) != 0 || len(builds.imageRefs) != 0 {
		t.Fatalf("missing compose project should not return build outputs, got statuses=%#v images=%#v", builds.statuses, builds.imageRefs)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("missing compose project must not run build commands:\n%s", strings.Join(fake.Seen, "\n"))
	}

	unsafeProject := "-bad"
	pg.ComposeProject = &unsafeProject
	builds, err = w.buildServices(ctx, pg, ps, domain.Marquee{Name: "local"})
	if err == nil || !strings.Contains(err.Error(), "unsafe compose project") {
		t.Fatalf("expected unsafe compose project error, got %v", err)
	}
	if len(builds.statuses) != 0 || len(builds.imageRefs) != 0 {
		t.Fatalf("unsafe compose project should not return build outputs, got statuses=%#v images=%#v", builds.statuses, builds.imageRefs)
	}
	if len(fake.Seen) != 0 {
		t.Fatalf("unsafe compose project must not run build commands:\n%s", strings.Join(fake.Seen, "\n"))
	}
}

func TestDeployPlaygroundStopsWhenLifecycleIsSuperseded(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "superseded-spec",
		BaseComposeYAML: "services:\n  web:\n    image: nginx:alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	project := "superseded--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "superseded-pg",
		Status:         domain.StatusPending,
		PlayspecID:     ps.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec := &stopDuringDeployExecutor{store: st, playgroundID: pg.ID}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}
	mq := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	updated, err := w.DeployPlayground(ctx, pg, ps, &mq)
	if err == nil || !strings.Contains(err.Error(), "deployment was superseded") {
		t.Fatalf("expected superseded deployment error, got updated=%#v err=%v", updated, err)
	}
	persisted, err := st.GetPlayground(ctx, "superseded-pg")
	if err != nil {
		t.Fatalf("get persisted playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("superseded deploy must preserve stopped status, got %#v", persisted)
	}
	seen := strings.Join(exec.Seen, "\n")
	if strings.Contains(seen, "write:/opt/fibe/playgrounds/"+project+"/compose.yml") ||
		strings.Contains(seen, "docker compose -f compose.yml -p '"+project+"' up") {
		t.Fatalf("superseded deploy should not upload or compose-up runtime artifacts, saw:\n%s", seen)
	}
}

func TestDeployPlaygroundStopsRemoteComposeWhenSupersededAfterComposeUp(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "compose-up-superseded-spec",
		BaseComposeYAML: "services:\n  web:\n    image: nginx:alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	project := "compose-up-superseded--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "compose-up-superseded-pg",
		Status:         domain.StatusPending,
		PlayspecID:     ps.ID,
		ComposeProject: &project,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	exec := &stopDuringDeployExecutor{store: st, playgroundID: pg.ID, mutateOn: " up -d --remove-orphans "}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}
	mq := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	updated, err := w.DeployPlayground(ctx, pg, ps, &mq)
	if err == nil || !strings.Contains(err.Error(), "deployment was superseded") {
		t.Fatalf("expected superseded deployment error, got updated=%#v err=%v", updated, err)
	}
	persisted, err := st.GetPlayground(ctx, "compose-up-superseded-pg")
	if err != nil {
		t.Fatalf("get persisted playground: %v", err)
	}
	if persisted.Status != domain.StatusStopped {
		t.Fatalf("superseded deploy must preserve stopped status, got %#v", persisted)
	}
	seen := strings.Join(exec.Seen, "\n")
	if !strings.Contains(seen, "docker compose -f compose.yml -p 'compose-up-superseded--1' stop") {
		t.Fatalf("superseded deploy after compose up should stop local compose, saw:\n%s", seen)
	}
	if strings.Contains(seen, "ps --all --format json") {
		t.Fatalf("superseded deploy should not continue into runtime observation:\n%s", seen)
	}
}

func TestDeployPlaygroundStopsBeforeRuntimeWhenCreationStepSaveFails(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "creation-step-save-fail-spec",
		BaseComposeYAML: "services:\n  web:\n    image: nginx:alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	project := "creation-step-save-fail--1"
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "creation-step-save-fail-pg",
		Status:         domain.StatusPending,
		PlayspecID:     ps.ID,
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
	if _, err := rawDB.ExecContext(ctx, `CREATE TRIGGER fail_host_prereq_step_save BEFORE UPDATE ON playgrounds
WHEN NEW.creation_steps_json LIKE '%host_prerequisites%' AND OLD.creation_steps_json NOT LIKE '%host_prerequisites%'
BEGIN
	SELECT RAISE(ABORT, 'forced creation step save failure');
END`); err != nil {
		t.Fatalf("create update trigger: %v", err)
	}

	exec := &runtimetest.FakeExecutor{}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: exec}}
	mq := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	updated, err := w.DeployPlayground(ctx, pg, ps, &mq)
	if err == nil || !strings.Contains(err.Error(), "forced creation step save failure") {
		t.Fatalf("expected creation-step save failure, got updated=%#v err=%v", updated, err)
	}
	if len(exec.Seen) != 0 {
		t.Fatalf("runtime commands must not run after progress persistence fails:\n%s", strings.Join(exec.Seen, "\n"))
	}
}

func TestDeploymentProgressPreservesCurrentExpiration(t *testing.T) {
	fixture := newDeploymentExpirationFixture(t, "deploy-progress-expiration")
	progress := fixture.playground
	progress.Status = domain.StatusInProgress
	progress.GeneratedComposeYAML = "services:\n  web:\n    image: alpine\n"

	w := Worker{DB: fixture.store}
	saved, err := w.saveDeploymentProgress(fixture.ctx, progress)
	if err != nil {
		t.Fatalf("save deployment progress: %v", err)
	}
	assertPlaygroundExpiry(t, "deployment progress", saved, fixture.extendedExpiry)
	persisted, err := fixture.store.GetPlayground(fixture.ctx, "deploy-progress-expiration")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	assertPlaygroundExpiry(t, "persisted deployment progress", persisted, fixture.extendedExpiry)
	if persisted.Status != domain.StatusInProgress || persisted.GeneratedComposeYAML == "" {
		t.Fatalf("deployment progress should still save runtime progress, got %#v", persisted)
	}
}

func TestDeploymentFailurePreservesCurrentExpiration(t *testing.T) {
	fixture := newDeploymentExpirationFixture(t, "deploy-failure-expiration")
	progress := fixture.playground
	progress.Status = domain.StatusInProgress

	w := Worker{DB: fixture.store}
	failed, err := w.failDeployment(fixture.ctx, progress, errors.New("compose failed"), []string{"web"})
	if err == nil {
		t.Fatal("expected deployment failure error")
	}
	assertPlaygroundExpiry(t, "deployment failure", failed, fixture.extendedExpiry)
	persisted, err := fixture.store.GetPlayground(fixture.ctx, "deploy-failure-expiration")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	assertPlaygroundExpiry(t, "persisted deployment failure", persisted, fixture.extendedExpiry)
	if persisted.Status != domain.StatusError || persisted.ErrorMessage == nil {
		t.Fatalf("deployment failure should still save error state, got %#v", persisted)
	}
}

func TestDeployPlaygroundGeneratesInternalPasswordAndAuthMiddleware(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	project := "auth--1"
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"ps --all --format json": {Stdout: `[{"Service":"web","State":"running","Health":"healthy","ExitCode":0}]`},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "auth-spec",
		BaseComposeYAML: `services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      fibe.gg/visibility: internal
`,
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	domains := "example.test"
	acmeEmail := "ops@example.test"
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local-auth", Host: "127.0.0.1", User: "root", Port: 22, DomainsInput: &domains, AcmeEmail: &acmeEmail})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "auth-pg",
		PlayspecID:     ps.ID,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		ServiceBranches: map[string]any{
			"web": map[string]any{"auth_password": "service-password"},
		},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	updated, err := w.DeployPlayground(ctx, pg, ps, &mq)
	if err != nil {
		t.Fatalf("deploy playground: %v", err)
	}
	assertInternalAuthDeploy(t, updated)
	persisted, err := st.GetPlayground(ctx, "auth-pg")
	if err != nil {
		t.Fatalf("get persisted playground: %v", err)
	}
	assertPersistedInternalAuthDeploy(t, persisted, updated)
}

func TestObserveRuntimeWaitsForRoutedServicesToBecomeHealthy(t *testing.T) {
	assertObserveRuntimeTimeout(t, "unhealthy--1", `[{"Service":"web","State":"running","Health":"starting","ExitCode":0}]`, "health timeout")
}

func TestObserveRuntimeDoesNotAcceptEmptyRoutedServiceEvidence(t *testing.T) {
	assertObserveRuntimeTimeout(t, "empty-routed--1", `[]`, "empty routed service evidence")
}

func TestStartRuntimePlaygroundWaitsForRoutedServicesToBecomeHealthy(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	project := "start-observe--1"
	exec := &startRuntimeObserveExecutor{}
	w := Worker{
		DB:                     st,
		Runtime:                runtime.Checker{Executor: exec},
		RuntimeObserveTimeout:  100 * time.Millisecond,
		RuntimeObserveInterval: time.Millisecond,
	}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "start-observe-pg",
		Status:         domain.StatusStopped,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		Services:       []domain.PlaygroundServiceInfo{{Name: "web", Status: "stopped"}},
		ServiceURLs:    []domain.PlaygroundServiceURL{{Name: "web", URL: "https://start.example.test"}},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	stale := pg
	stale.Services = nil
	stale.ServiceURLs = nil
	updated, err := w.StartRuntimePlayground(ctx, stale, &mq)
	if err != nil {
		t.Fatalf("start runtime playground: %v", err)
	}
	if updated.Status != domain.StatusRunning {
		t.Fatalf("expected running playground, got %#v", updated)
	}
	if exec.inspectCount < 2 {
		t.Fatalf("expected start to poll until routed service was healthy, inspect count=%d", exec.inspectCount)
	}
	persisted, err := st.GetPlayground(ctx, "start-observe-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusRunning || len(persisted.ServiceURLs) != 1 || persisted.ServiceURLs[0].Running == nil || !*persisted.ServiceURLs[0].Running || persisted.ServiceURLs[0].Health != "healthy" {
		t.Fatalf("expected persisted healthy routed service, got %#v", persisted)
	}
}

func TestStartRuntimePlaygroundProbesRouteWhenServiceHasNoHealthcheck(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	project := "start-route-probe--1"
	fake := &runtimetest.FakeExecutor{
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_INSPECT": {Stdout: `[{"Service":"web","Image":"alpine","State":"running","Health":"","ExitCode":0}]`},
		},
	}
	probe := &sequenceRouteProbeClient{statuses: []int{http.StatusBadGateway, http.StatusOK}}
	w := Worker{
		DB:                     st,
		Runtime:                runtime.Checker{Executor: fake},
		RuntimeObserveTimeout:  100 * time.Millisecond,
		RuntimeObserveInterval: time.Millisecond,
		RouteProbeClient:       probe,
	}
	mq, err := createTestRuntimeMarquee(t, ctx, st, domain.Marquee{Name: "local", Host: "127.0.0.1", User: "root", Port: 22, SSHPrivateKey: "test", Status: "active"})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:           "start-route-probe-pg",
		Status:         domain.StatusStopped,
		MarqueeID:      &mq.ID,
		ComposeProject: &project,
		Services:       []domain.PlaygroundServiceInfo{{Name: "web", Status: "stopped"}},
		ServiceURLs:    []domain.PlaygroundServiceURL{{Name: "web", URL: "https://start.example.test"}},
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	updated, err := w.StartRuntimePlayground(ctx, pg, &mq)
	if err != nil {
		t.Fatalf("start runtime playground: %v", err)
	}
	if updated.Status != domain.StatusRunning {
		t.Fatalf("expected running playground, got %#v", updated)
	}
	if probe.calls < 2 {
		t.Fatalf("expected route probe to retry through 502, calls=%d", probe.calls)
	}
}

type sequenceRouteProbeClient struct {
	statuses []int
	calls    int
}

func (c *sequenceRouteProbeClient) Do(*http.Request) (*http.Response, error) {
	status := http.StatusOK
	if c.calls < len(c.statuses) {
		status = c.statuses[c.calls]
	}
	c.calls++
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func TestDeployPlaygroundRecordsFailedCreationStep(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	project := "source-fail--1"
	secretURL := "https://x-access-token:ghp_secret@github.com/acme/demo.git"
	secretHeader := "Authorization: Basic eC1hY2Nlc3MtdG9rZW46Z2hwX3NlY3JldA=="
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{"FIBE_DISTILLED_SOURCE_SYNC": errors.New("source checkout failed")},
		ResultContains: map[string]runtime.CommandResult{
			"FIBE_DISTILLED_SOURCE_SYNC": {Stderr: "fatal: Authentication failed for '" + secretURL + "'\n" + secretHeader + "\n"},
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "source-fail-spec",
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
	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "source-fail-pg", PlayspecID: ps.ID, ComposeProject: &project})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	mq := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	if _, err := w.DeployPlayground(ctx, pg, ps, &mq); err == nil {
		t.Fatalf("expected deploy failure")
	}
	persisted, err := st.GetPlayground(ctx, "source-fail-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	if persisted.Status != domain.StatusError {
		t.Fatalf("expected error status, got %#v", persisted)
	}
	assertSourceSyncFailureDetailsRedacted(t, persisted.ErrorDetails)
	assertSourceSyncFailureStatus(t, persisted)
	assertRuntimeWorkspacePreparedBeforeSourceSync(t, fake.Seen, project)
}

func TestDeployPlaygroundFailsBeforeSourceSyncWhenHostPrerequisitesFail(t *testing.T) {
	ctx, st := openWorkerTestStore(t)
	project := "host-check-fail--1"
	fake := &runtimetest.FakeExecutor{
		ErrorContains: map[string]error{
			"docker:ping": errors.New("docker compose version missing"),
		},
	}
	w := Worker{DB: st, Runtime: runtime.Checker{Executor: fake}}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "host-check-fail-spec",
		BaseComposeYAML: "services:\n  web:\n    image: nginx\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "host-check-fail-pg", PlayspecID: ps.ID, ComposeProject: &project})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	mq := domain.Marquee{ID: 7, Name: "local", Host: "127.0.0.1", User: "root", Port: 22}

	if _, err := w.DeployPlayground(ctx, pg, ps, &mq); err == nil || !strings.Contains(err.Error(), "docker compose version") {
		t.Fatalf("expected host prerequisite failure, got %v", err)
	}
	persisted, err := st.GetPlayground(ctx, "host-check-fail-pg")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	assertCreationStep(t, persisted.CreationSteps, "compose_render", "completed")
	assertCreationFailureStatus(t, persisted, "host_prerequisites", "Check host")
	assertSourceSyncNotSeen(t, fake.Seen, "host prerequisite failure")
}

func TestDeployPlaygroundRequiresMarquee(t *testing.T) {
	w := Worker{}
	_, err := w.DeployPlayground(context.Background(), domain.Playground{Name: "no-host"}, domain.Playspec{}, nil)
	if err == nil || !strings.Contains(err.Error(), "marquee is required") {
		t.Fatalf("expected required marquee error, got %v", err)
	}
}

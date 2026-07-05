package storage

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

func TestEmbeddedMigrationsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	assertMigrationVersionRows(t, st, 1)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	assertMigrationVersionRows(t, reopened, 1)
}

func TestMigrationManifestRejectsDuplicateVersions(t *testing.T) {
	err := rejectDuplicateMigrationVersions([]sqlMigration{
		{version: 1, name: "00001_init.sql"},
		{version: 2, name: "00002_one.sql"},
		{version: 2, name: "00002_two.sql"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate migration version 2") {
		t.Fatalf("expected duplicate migration version error, got %v", err)
	}
}

// assertMigrationVersion verifies the latest applied schema migration version.
func assertMigrationVersion(t *testing.T, st *DB, expected int64) {
	t.Helper()
	var version int64
	if err := st.db.QueryRowContext(context.Background(), `SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&version); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != expected {
		t.Fatalf("expected migrated schema version %d, got %d", expected, version)
	}
}

// assertMigrationVersionRows verifies each migration version has one applied row.
func assertMigrationVersionRows(t *testing.T, st *DB, expected int) {
	t.Helper()
	var count int
	if err := st.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM goose_db_version WHERE is_applied=1`).Scan(&count); err != nil {
		t.Fatalf("count migration versions: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d applied migration rows, got %d", expected, count)
	}
	assertMigrationVersion(t, st, int64(expected))
}

func assertStoreErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected store error containing %q, got %v", want, err)
	}
}

func setStoredJSONNull(t *testing.T, ctx context.Context, st *DB, query string, args ...any) {
	t.Helper()
	if _, err := st.db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("set stored JSON null: %v", err)
	}
}

func deleteTestPlayspec(t *testing.T, ctx context.Context, st *DB, id int64) {
	t.Helper()
	if _, err := st.db.ExecContext(ctx, `DELETE FROM playspecs WHERE id=?`, id); err != nil {
		t.Fatalf("delete test playspec: %v", err)
	}
}

func ensureConfiguredMarquee(t *testing.T, ctx context.Context, st *DB, marquee domain.Marquee) domain.Marquee {
	t.Helper()
	got, err := st.EnsureConfiguredMarquee(ctx, marquee)
	if err != nil {
		t.Fatalf("ensure configured marquee: %v", err)
	}
	return got
}

func seedAsyncOperations(t *testing.T, ctx context.Context, st *DB, operations []domain.AsyncOperation) {
	t.Helper()
	for _, op := range operations {
		if _, err := st.CreateAsync(ctx, op); err != nil {
			t.Fatalf("create async %s: %v", op.ID, err)
		}
	}
}

func assertAsyncInterrupted(t *testing.T, ctx context.Context, st *DB, id string) {
	t.Helper()
	op, err := st.GetAsync(ctx, id)
	if err != nil {
		t.Fatalf("get async %s: %v", id, err)
	}
	if op.Status != domain.AsyncError || op.Error == nil || op.Error.Code != "INTERRUPTED" {
		t.Fatalf("expected interrupted async error for %s, got %#v", id, op)
	}
}

func assertAsyncSuccessPayload(t *testing.T, ctx context.Context, st *DB, id string, key string, want any) {
	t.Helper()
	op, err := st.GetAsync(ctx, id)
	if err != nil {
		t.Fatalf("get async %s: %v", id, err)
	}
	if op.Status != domain.AsyncSuccess || op.Payload[key] != want {
		t.Fatalf("success operation should keep %s=%#v, got %#v", key, want, op)
	}
}

func assertRepairMetadata(t *testing.T, got domain.Playground, wantID int64, wantReason string, wantLock time.Time) {
	t.Helper()
	if got.ID != wantID || got.PlayguardRepairReason == nil || *got.PlayguardRepairReason != wantReason {
		t.Fatalf("repair reason did not persist: %#v", got)
	}
	if got.PlayguardRepairLockUntil == nil || !got.PlayguardRepairLockUntil.Equal(wantLock) {
		t.Fatalf("repair lock did not persist: got=%v want=%v", got.PlayguardRepairLockUntil, wantLock)
	}
	if got.NeedsRecreation == nil || !*got.NeedsRecreation {
		t.Fatalf("needs recreation did not persist: %#v", got.NeedsRecreation)
	}
}

func sanitizeTestName(name string) string {
	return strings.NewReplacer(" ", "-", "\"", "", ":", "").Replace(name)
}

func TestSaveMethodsFailWhenRowMissing(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	id := int64(12345)
	for _, tt := range []struct {
		name string
		save func() error
	}{
		{
			name: "marquee",
			save: func() error {
				_, err := st.saveMarquee(ctx, domain.Marquee{ID: id, Name: "missing-marquee", Port: 22})
				return err
			},
		},
		{
			name: "prop",
			save: func() error {
				_, err := st.SaveProp(ctx, domain.Prop{ID: id, Name: "missing-prop"})
				return err
			},
		},
		{
			name: "playspec",
			save: func() error {
				_, err := st.SavePlayspec(ctx, domain.Playspec{ID: &id, Name: "missing-playspec", BaseComposeYAML: "services: {}\n"})
				return err
			},
		},
		{
			name: "playspec nil id",
			save: func() error {
				_, err := st.SavePlayspec(ctx, domain.Playspec{Name: "missing-playspec"})
				return err
			},
		},
		{
			name: "playground",
			save: func() error {
				_, err := st.SavePlayground(ctx, domain.Playground{ID: id, Name: "missing-playground", Status: domain.StatusRunning})
				return err
			},
		},
		{
			name: "build record",
			save: func() error {
				_, err := st.SaveBuildRecord(ctx, domain.BuildRecord{ID: id, ServiceName: "web", Status: "success"})
				return err
			},
		},
		{
			name: "async",
			save: func() error {
				return st.SaveAsync(ctx, domain.AsyncOperation{ID: "missing-async", Status: domain.AsyncSuccess})
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.save(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}
		})
	}
}

func TestIsUniqueConstraintUsesSQLiteErrorCodes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prop := domain.Prop{Name: "unique-prop", RepositoryURL: "https://github.com/acme/unique-prop"}
	if _, err := st.CreateProp(ctx, prop); err != nil {
		t.Fatalf("create prop: %v", err)
	}
	if _, err := st.CreateProp(ctx, prop); !IsUniqueConstraint(err) {
		t.Fatalf("expected typed unique constraint, got %v", err)
	}
	if IsUniqueConstraint(errors.New("UNIQUE constraint failed: props.name")) {
		t.Fatalf("plain error strings must not classify as SQLite uniqueness")
	}
}

func TestCreatePropInfersProviderFromGitHubRepositoryIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cases := []struct {
		name string
		repo string
		want string
	}{
		{name: "github https", repo: "https://github.com/acme/api.git", want: "github"},
		{name: "github www", repo: "https://www.github.com/acme/api.git", want: "github"},
		{name: "github scp", repo: "git@github.com:acme/api.git", want: "github"},
		{name: "github ssh url", repo: "ssh://git@github.com/acme/api.git", want: "github"},
		{name: "github host invalid repo path", repo: "https://github.com/acme/api/extra", want: "git"},
		{name: "lookalike domain", repo: "https://notgithub.com/acme/api.git", want: "git"},
		{name: "github suffix domain", repo: "https://github.com.evil/acme/api.git", want: "git"},
		{name: "generic ssh url", repo: "ssh://git@example.com/acme/api.git", want: "git"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prop, err := st.CreateProp(ctx, domain.Prop{
				Name:          slug(tc.name),
				RepositoryURL: tc.repo,
			})
			if err != nil {
				t.Fatalf("create prop: %v", err)
			}
			if prop.Provider != tc.want {
				t.Fatalf("provider = %q, want %q", prop.Provider, tc.want)
			}
		})
	}
}

func TestSlugNormalizesGeneratedNames(t *testing.T) {
	cases := map[string]string{
		" API Service ":    "api-service",
		"api---service":    "api-service",
		"api_service.v2":   "api-service-v2",
		"Привіт":           "fibe-distilled",
		"../secrets":       "secrets",
		"already-clean-42": "already-clean-42",
	}
	for raw, want := range cases {
		if got := slug(raw); got != want {
			t.Fatalf("slug(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPropPersistenceTrimsScalarWhitespace(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prop, err := st.CreateProp(ctx, domain.Prop{
		Name:          " trimmed-repo ",
		RepositoryURL: " https://github.com/acme/trimmed-repo.git ",
		DefaultBranch: " main ",
		Provider:      " GitHub ",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	if prop.RepositoryURL != "https://github.com/acme/trimmed-repo.git" {
		t.Fatalf("repository_url = %q", prop.RepositoryURL)
	}
	if prop.Name != "trimmed-repo" || prop.DefaultBranch != "main" {
		t.Fatalf("trimmed prop fields = %#v", prop)
	}
	if prop.Provider != "github" {
		t.Fatalf("provider = %q", prop.Provider)
	}

	prop.RepositoryURL = " ssh://git.example.test/acme/trimmed-repo.git "
	prop.Name = " renamed-trimmed-repo "
	prop.DefaultBranch = " trunk "
	prop.Provider = " Git "
	updated, err := st.SaveProp(ctx, prop)
	if err != nil {
		t.Fatalf("save prop: %v", err)
	}
	if updated.RepositoryURL != "ssh://git.example.test/acme/trimmed-repo.git" {
		t.Fatalf("updated repository_url = %q", updated.RepositoryURL)
	}
	if updated.Name != "renamed-trimmed-repo" || updated.DefaultBranch != "trunk" {
		t.Fatalf("updated trimmed prop fields = %#v", updated)
	}
	if updated.Provider != "git" {
		t.Fatalf("updated provider = %q", updated.Provider)
	}
}

func TestPlayspecAndPlaygroundPersistenceTrimsNames(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	playspec, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            " trimmed-spec ",
		BaseComposeYAML: "services:\n  web:\n    image: alpine\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	if playspec.Name != "trimmed-spec" {
		t.Fatalf("playspec name = %q", playspec.Name)
	}

	playspec.Name = " renamed-spec "
	playspec, err = st.SavePlayspec(ctx, playspec)
	if err != nil {
		t.Fatalf("save playspec: %v", err)
	}
	if playspec.Name != "renamed-spec" {
		t.Fatalf("updated playspec name = %q", playspec.Name)
	}

	playground, err := st.CreatePlayground(ctx, domain.Playground{
		Name:       " trimmed-playground ",
		PlayspecID: playspec.ID,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	if playground.Name != "trimmed-playground" {
		t.Fatalf("playground name = %q", playground.Name)
	}

	playground.Name = " renamed-playground "
	playground, err = st.SavePlayground(ctx, playground)
	if err != nil {
		t.Fatalf("save playground: %v", err)
	}
	if playground.Name != "renamed-playground" {
		t.Fatalf("updated playground name = %q", playground.Name)
	}
}

func TestEnsureConfiguredMarqueeUpsertsStableRow(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	domainOne := "one.example.test"
	https := true
	first := ensureConfiguredMarquee(t, ctx, st, domain.Marquee{
		Name:          ConfiguredMarqueeName,
		Host:          "host-one",
		Port:          22,
		User:          "root",
		DomainsInput:  &domainOne,
		HTTPSEnabled:  &https,
		SSHPrivateKey: "key-one",
		Status:        "active",
	})

	domainTwo := "two.example.test"
	second := ensureConfiguredMarquee(t, ctx, st, domain.Marquee{
		Name:          ConfiguredMarqueeName,
		Host:          "host-two",
		Port:          2222,
		User:          "deploy",
		DomainsInput:  &domainTwo,
		HTTPSEnabled:  &https,
		SSHPrivateKey: "key-two",
		Status:        "active",
	})
	if second.ID != first.ID || second.Host != "host-two" || second.User != "deploy" || second.Port != 2222 || second.DomainsInput == nil || *second.DomainsInput != domainTwo {
		t.Fatalf("configured marquee was not updated in place: first=%#v second=%#v", first, second)
	}

	second.Status = "inactive"
	if _, err := st.saveMarquee(ctx, second); err != nil {
		t.Fatalf("mark configured marquee inactive: %v", err)
	}
	restored := ensureConfiguredMarquee(t, ctx, st, domain.Marquee{
		Name:         ConfiguredMarqueeName,
		Host:         "host-two",
		Port:         2222,
		User:         "deploy",
		DomainsInput: &domainTwo,
		HTTPSEnabled: &https,
		Status:       "inactive",
	})
	if restored.Status != "active" {
		t.Fatalf("configured marquee status should be owned by startup config, got %q", restored.Status)
	}
}

func TestGetRuntimeMarqueeUsesOnlyConfiguredRow(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.createMarquee(ctx, domain.Marquee{Name: "legacy", Host: "legacy.example.test", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create legacy marquee: %v", err)
	}
	legacy, err := st.GetMarquee(ctx, "legacy")
	if err != nil {
		t.Fatalf("get legacy marquee: %v", err)
	}
	if legacy.Status != "" {
		t.Fatalf("legacy marquee status should not be fabricated, got %q", legacy.Status)
	}
	beforeConfigured, found, err := st.GetRuntimeMarquee(ctx)
	if err != nil {
		t.Fatalf("runtime marquee before configured = %#v, %v, %v", beforeConfigured, found, err)
	}
	if found {
		t.Fatalf("runtime marquee should ignore legacy rows before configured row exists: %#v", beforeConfigured)
	}

	configured, err := st.createMarquee(ctx, domain.Marquee{Name: ConfiguredMarqueeName, Host: "configured.example.test", User: "root", Port: 22})
	if err != nil {
		t.Fatalf("create configured marquee: %v", err)
	}
	afterConfigured, found, err := st.GetRuntimeMarquee(ctx)
	if err != nil || !found {
		t.Fatalf("runtime marquee after configured = %#v, %v, %v", afterConfigured, found, err)
	}
	if afterConfigured.ID != configured.ID {
		t.Fatalf("runtime marquee should use only configured row, got %#v want id %d", afterConfigured, configured.ID)
	}
}

func TestMarqueeStorageForcesHTTPSDefaults(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	domainName := "apps.example.test"
	https := false
	mq, err := st.createMarquee(ctx, domain.Marquee{
		Name:          "local",
		Host:          "127.0.0.1",
		User:          "root",
		DomainsInput:  &domainName,
		HTTPSEnabled:  &https,
		SSHPrivateKey: "test",
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("create marquee: %v", err)
	}
	if mq.HTTPSEnabled == nil || !*mq.HTTPSEnabled {
		t.Fatalf("marquee should be forced to HTTPS: %#v", mq)
	}
	if mq.TLSCertificateSource == nil || *mq.TLSCertificateSource != "automatic" {
		t.Fatalf("marquee should default to automatic TLS: %#v", mq)
	}
}

func TestCreatePlaygroundReturnsUpdatedAt(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	created, err := st.CreatePlayground(ctx, domain.Playground{Name: "created-timestamps", Status: domain.StatusRunning})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created playground should return both timestamps, got created_at=%s updated_at=%s", created.CreatedAt, created.UpdatedAt)
	}
	if !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("fresh playground timestamps should match, got created_at=%s updated_at=%s", created.CreatedAt, created.UpdatedAt)
	}
}

func TestRecoverInterruptedAsyncOperations(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedAsyncOperations(t, ctx, st, []domain.AsyncOperation{
		{ID: "queued", Status: domain.AsyncQueued},
		{ID: "running", Status: domain.AsyncRunning},
		{ID: "success", Status: domain.AsyncSuccess, Payload: map[string]any{"ok": true}},
	})

	count, err := st.RecoverInterruptedAsyncOperations(ctx)
	if err != nil {
		t.Fatalf("recover interrupted async: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 recovered operations, got %d", count)
	}
	for _, id := range []string{"queued", "running"} {
		assertAsyncInterrupted(t, ctx, st, id)
	}
	assertAsyncSuccessPayload(t, ctx, st, "success", "ok", true)
}

func TestStoreFailsClosedOnUnencodableCanonicalJSON(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "bad-services-json",
		BaseComposeYAML: "services: {}\n",
		Services:        []any{map[string]any{"bad": math.Inf(1)}},
	}); err == nil || !strings.Contains(err.Error(), "playspecs.services_json") {
		t.Fatalf("expected playspec services encode error, got %v", err)
	}

	if _, err := st.CreatePlayground(ctx, domain.Playground{
		Name:         "bad-error-details-json",
		Status:       domain.StatusRunning,
		ErrorDetails: map[string]any{"bad": math.Inf(1)},
	}); err == nil || !strings.Contains(err.Error(), "playgrounds.error_details_json") {
		t.Fatalf("expected playground error details encode error, got %v", err)
	}

	if _, err := st.CreateAsync(ctx, domain.AsyncOperation{
		ID:      "bad-payload-json",
		Payload: map[string]any{"bad": math.Inf(1)},
	}); err == nil || !strings.Contains(err.Error(), "async_operations.payload_json") {
		t.Fatalf("expected async payload encode error, got %v", err)
	}
}

func TestStoreFailsClosedOnCorruptCanonicalJSON(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "corrupt-json", Status: domain.StatusRunning})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE playgrounds SET error_details_json='{' WHERE id=?`, pg.ID); err != nil {
		t.Fatalf("corrupt playground JSON: %v", err)
	}
	_, err = st.GetPlayground(ctx, "corrupt-json")
	if err == nil || !strings.Contains(err.Error(), "playgrounds.error_details_json") {
		t.Fatalf("expected corrupt playground JSON error, got %v", err)
	}

	ps, err := st.CreatePlayspec(ctx, domain.Playspec{Name: "corrupt-services", BaseComposeYAML: "services: {}\n"})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE playspecs SET services_json='{' WHERE id=?`, *ps.ID); err != nil {
		t.Fatalf("corrupt playspec JSON: %v", err)
	}
	_, err = st.GetPlayspec(ctx, "corrupt-services")
	if err == nil || !strings.Contains(err.Error(), "playspecs.services_json") {
		t.Fatalf("expected corrupt playspec JSON error, got %v", err)
	}
}

func TestStoreFailsClosedOnStoredJSONNull(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "null-json-playground", Status: domain.StatusRunning})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	setStoredJSONNull(t, ctx, st, `UPDATE playgrounds SET error_details_json='null' WHERE id=?`, pg.ID)
	_, err = st.GetPlayground(ctx, pg.Name)
	assertStoreErrorContains(t, err, "playgrounds.error_details_json")

	prop, err := st.CreateProp(ctx, domain.Prop{Name: "null-json-prop", RepositoryURL: "https://github.com/acme/null-json-prop", Provider: "github", Status: "active", DefaultBranch: "main"})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	setStoredJSONNull(t, ctx, st, `UPDATE props SET branches_json='null' WHERE id=?`, prop.ID)
	_, err = st.GetProp(ctx, prop.Name)
	assertStoreErrorContains(t, err, "props.branches_json")

	ps, err := st.CreatePlayspec(ctx, domain.Playspec{Name: "null-json-playspec", BaseComposeYAML: "services: {}\n"})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	setStoredJSONNull(t, ctx, st, `UPDATE playspecs SET services_json='null' WHERE id=?`, *ps.ID)
	_, err = st.GetPlayspec(ctx, ps.Name)
	assertStoreErrorContains(t, err, "playspecs.services_json")

	if _, err := st.CreateAsync(ctx, domain.AsyncOperation{ID: "null-json-async-payload", Status: domain.AsyncSuccess, Payload: map[string]any{"ok": true}}); err != nil {
		t.Fatalf("create async payload: %v", err)
	}
	setStoredJSONNull(t, ctx, st, `UPDATE async_operations SET payload_json='null' WHERE id='null-json-async-payload'`)
	_, err = st.GetAsync(ctx, "null-json-async-payload")
	assertStoreErrorContains(t, err, "async_operations.payload_json")

	if _, err := st.CreateAsync(ctx, domain.AsyncOperation{ID: "null-json-async-error", Status: domain.AsyncError, Error: &domain.APIError{Code: "BROKEN", Message: "broken"}}); err != nil {
		t.Fatalf("create async: %v", err)
	}
	setStoredJSONNull(t, ctx, st, `UPDATE async_operations SET error_json='null' WHERE id='null-json-async-error'`)
	_, err = st.GetAsync(ctx, "null-json-async-error")
	assertStoreErrorContains(t, err, "async_operations.error_json")
}

func TestPlayspecsReferencingPropUsesExactComposeRepoLabels(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prop, err := st.CreateProp(ctx, domain.Prop{
		Name:          "api",
		RepositoryURL: "https://github.com/acme/api",
		Provider:      "github",
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	if _, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name: "substring-only",
		BaseComposeYAML: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/api-extra
`,
	}); err != nil {
		t.Fatalf("create substring playspec: %v", err)
	}
	names, err := st.PlayspecsReferencingProp(ctx, prop)
	if err != nil {
		t.Fatalf("list references after substring playspec: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("substring repo URLs must not lock prop deletion, got %#v", names)
	}

	for _, ps := range []domain.Playspec{
		{
			Name: "exact-map-label",
			BaseComposeYAML: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/api
`,
		},
		{
			Name: "canonical-map-label",
			BaseComposeYAML: `services:
  api:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/api.git/
`,
		},
		{
			Name: "padded-map-label",
			BaseComposeYAML: `services:
  api:
    image: alpine
    labels:
      " fibe.gg/repo_url ": " https://github.com/acme/api "
`,
		},
		{
			Name: "exact-list-label",
			BaseComposeYAML: `services:
  worker:
    image: alpine
    labels:
      - fibe.gg/repo_url=https://github.com/acme/api
`,
		},
		{
			Name:            "canonical-service-metadata",
			BaseComposeYAML: "services: {}\n",
			Services:        []any{map[string]any{"name": "api", "repo_url": "git@github.com:acme/api.git"}},
		},
	} {
		if _, err := st.CreatePlayspec(ctx, ps); err != nil {
			t.Fatalf("create %s playspec: %v", ps.Name, err)
		}
	}

	names, err = st.PlayspecsReferencingProp(ctx, prop)
	if err != nil {
		t.Fatalf("list references after exact playspecs: %v", err)
	}
	want := []string{"exact-map-label", "canonical-map-label", "padded-map-label", "exact-list-label", "canonical-service-metadata"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("expected canonical repo references, got %#v want %#v", names, want)
	}
}

func TestPlayspecsReferencingPropFailsClosedOnNullServicesJSON(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prop, err := st.CreateProp(ctx, domain.Prop{
		Name:          "api-null-services",
		RepositoryURL: "https://github.com/acme/api-null-services",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	ps, err := st.CreatePlayspec(ctx, domain.Playspec{
		Name:            "null-services-reference-scan",
		BaseComposeYAML: "services: {}\n",
	})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE playspecs SET services_json='null' WHERE id=?`, *ps.ID); err != nil {
		t.Fatalf("set services JSON null: %v", err)
	}
	if _, err := st.PlayspecsReferencingProp(ctx, prop); err == nil || !strings.Contains(err.Error(), "playspecs.services_json") {
		t.Fatalf("expected services JSON null reference error, got %v", err)
	}
}

func TestPlayspecsReferencingPropFailsClosedOnMalformedCompose(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prop, err := st.CreateProp(ctx, domain.Prop{
		Name:          "api-malformed-compose",
		RepositoryURL: "https://github.com/acme/api-malformed-compose",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	cases := []struct {
		name    string
		compose string
		want    string
	}{
		{name: "top-level null", compose: "null\n", want: "expected mapping"},
		{name: "null services", compose: "services: null\n", want: "services must be a mapping"},
		{name: "null service body", compose: "services:\n  web: null\n", want: `service "web" must be a mapping`},
		{name: "blank service name", compose: "services:\n  \"\":\n    image: alpine\n", want: "service name is required"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ps, err := st.CreatePlayspec(ctx, domain.Playspec{
				Name:            "malformed-compose-" + sanitizeTestName(tc.name),
				BaseComposeYAML: tc.compose,
			})
			if err != nil {
				t.Fatalf("create playspec: %v", err)
			}
			_, err = st.PlayspecsReferencingProp(ctx, prop)
			assertStoreErrorContains(t, err, tc.want)
			deleteTestPlayspec(t, ctx, st, *ps.ID)
		})
	}
}

func TestPlayspecsReferencingPropRequiresPositiveServicePropID(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prop, err := st.CreateProp(ctx, domain.Prop{
		Name:          "api-by-id",
		RepositoryURL: "https://github.com/acme/api-by-id",
		Provider:      "github",
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	for _, ps := range []domain.Playspec{
		{
			Name:            "missing-prop-id",
			BaseComposeYAML: "services: {}\n",
			Services:        []any{map[string]any{"name": "web"}},
		},
		{
			Name:            "zero-prop-id",
			BaseComposeYAML: "services: {}\n",
			Services:        []any{map[string]any{"name": "web", "prop_id": 0}},
		},
		{
			Name:            "fractional-prop-id",
			BaseComposeYAML: "services: {}\n",
			Services:        []any{map[string]any{"name": "web", "prop_id": 1.5}},
		},
		{
			Name:            "matching-prop-id",
			BaseComposeYAML: "services: {}\n",
			Services:        []any{map[string]any{"name": "web", "prop_id": prop.ID}},
		},
	} {
		if _, err := st.CreatePlayspec(ctx, ps); err != nil {
			t.Fatalf("create %s playspec: %v", ps.Name, err)
		}
	}

	names, err := st.PlayspecsReferencingProp(ctx, domain.Prop{})
	if err != nil {
		t.Fatalf("list references for zero prop: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("zero-value prop must not match missing prop_id values, got %#v", names)
	}

	names, err = st.PlayspecsReferencingProp(ctx, prop)
	if err != nil {
		t.Fatalf("list references for persisted prop: %v", err)
	}
	if len(names) != 1 || names[0] != "matching-prop-id" {
		t.Fatalf("expected only exact positive prop_id reference, got %#v", names)
	}
}

func TestPositiveInt64FromAnyRejectsUnsafeFloatIDs(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int64
		ok    bool
	}{
		{name: "int64", value: int64(42), want: 42, ok: true},
		{name: "int", value: 42, want: 42, ok: true},
		{name: "safe float", value: float64(42), want: 42, ok: true},
		{name: "fractional float", value: 42.5},
		{name: "unsafe float", value: float64(1<<53) + 2},
		{name: "max int rounded float", value: float64(math.MaxInt64)},
		{name: "json number max int", value: json.Number("9223372036854775807"), want: math.MaxInt64, ok: true},
		{name: "string", value: "42", want: 42, ok: true},
		{name: "zero string", value: "0"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := positiveInt64FromAny(tc.value)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("positiveInt64FromAny(%#v) = %d, %v; want %d, %v", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestStoreFailsClosedOnCorruptTimestamps(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "corrupt-time", Status: domain.StatusRunning})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE playgrounds SET created_at='not-a-time' WHERE id=?`, pg.ID); err != nil {
		t.Fatalf("corrupt created_at: %v", err)
	}
	if _, err := st.GetPlayground(ctx, "corrupt-time"); err == nil || !strings.Contains(err.Error(), "playgrounds.created_at") {
		t.Fatalf("expected corrupt created_at error, got %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE playgrounds SET created_at=?, expires_at='also-not-a-time' WHERE id=?`, encodeTime(time.Now().UTC()), pg.ID); err != nil {
		t.Fatalf("corrupt expires_at: %v", err)
	}
	if _, err := st.GetPlayground(ctx, "corrupt-time"); err == nil || !strings.Contains(err.Error(), "playgrounds.expires_at") {
		t.Fatalf("expected corrupt expires_at error, got %v", err)
	}
}

func TestSavePlaygroundFailsClosedWhenCurrentRowIsUnreadable(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	pg, err := st.CreatePlayground(ctx, domain.Playground{Name: "unreadable-save", Status: domain.StatusRunning})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE playgrounds SET services_json='{' WHERE id=?`, pg.ID); err != nil {
		t.Fatalf("corrupt services_json: %v", err)
	}
	pg.Status = domain.StatusStopped
	if _, err := st.SavePlayground(ctx, pg); err == nil || !strings.Contains(err.Error(), "playgrounds.services_json") {
		t.Fatalf("expected current-row decode error, got %v", err)
	}

	var status string
	if err := st.db.QueryRowContext(ctx, `SELECT status FROM playgrounds WHERE id=?`, pg.ID).Scan(&status); err != nil {
		t.Fatalf("read raw status: %v", err)
	}
	if status != domain.StatusRunning {
		t.Fatalf("failed save must not overwrite current row, got status %q", status)
	}
}

func TestPlayspecLockDecorationFailsClosed(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ps, err := st.CreatePlayspec(ctx, domain.Playspec{Name: "lock-status", BaseComposeYAML: "services: {}\n"})
	if err != nil {
		t.Fatalf("create playspec: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `ALTER TABLE playgrounds RENAME TO playgrounds_broken`); err != nil {
		t.Fatalf("break playground count table: %v", err)
	}

	for _, tt := range []struct {
		name string
		read func() error
	}{
		{
			name: "get",
			read: func() error {
				_, err := st.GetPlayspec(ctx, "lock-status")
				return err
			},
		},
		{
			name: "list",
			read: func() error {
				_, err := st.ListPlayspecs(ctx)
				return err
			},
		},
		{
			name: "save",
			read: func() error {
				_, err := st.SavePlayspec(ctx, ps)
				return err
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.read(); err == nil || !strings.Contains(err.Error(), "playgrounds") {
				t.Fatalf("expected playground count error, got %v", err)
			}
		})
	}
}

func TestServerIDPersistsAcrossOpenHandles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fibe-distilled.sqlite3")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	first, err := st.ServerID(ctx)
	if err != nil {
		t.Fatalf("server id: %v", err)
	}
	if first == "" {
		t.Fatalf("expected generated server id")
	}
	second, err := st.ServerID(ctx)
	if err != nil {
		t.Fatalf("server id again: %v", err)
	}
	if second != first {
		t.Fatalf("server id must be stable within one handle: first=%q second=%q", first, second)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	afterReopen, err := reopened.ServerID(ctx)
	if err != nil {
		t.Fatalf("server id after reopen: %v", err)
	}
	if afterReopen != first {
		t.Fatalf("server id must persist across reopen: first=%q reopened=%q", first, afterReopen)
	}
}

func TestPlaygroundRepairMetadataPersists(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reason := "image_drift"
	lockUntil := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	needsRecreation := true
	pg, err := st.CreatePlayground(ctx, domain.Playground{
		Name:                     "repair-metadata",
		Status:                   domain.StatusRunning,
		PlayguardRepairReason:    &reason,
		PlayguardRepairLockUntil: &lockUntil,
		NeedsRecreation:          &needsRecreation,
	})
	if err != nil {
		t.Fatalf("create playground: %v", err)
	}

	got, err := st.GetPlayground(ctx, "repair-metadata")
	if err != nil {
		t.Fatalf("get playground: %v", err)
	}
	assertRepairMetadata(t, got, pg.ID, reason, lockUntil)
}

func TestRecoverInterruptedPlaygrounds(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	started := time.Now().UTC().Add(-time.Minute)
	for _, pg := range []domain.Playground{
		{Name: "pending-pg", Status: domain.StatusPending},
		{
			Name:   "in-progress-pg",
			Status: domain.StatusInProgress,
			CreationSteps: []domain.PlaygroundCreationStep{{
				Name:      "compose_deploy",
				Label:     "Deploy compose",
				Status:    "running",
				StartedAt: &started,
			}},
		},
		{Name: "running-pg", Status: domain.StatusRunning},
	} {
		if _, err := st.CreatePlayground(ctx, pg); err != nil {
			t.Fatalf("create playground %s: %v", pg.Name, err)
		}
	}

	count, err := st.RecoverInterruptedPlaygrounds(ctx)
	if err != nil {
		t.Fatalf("recover interrupted playgrounds: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 recovered playgrounds, got %d", count)
	}

	for _, name := range []string{"pending-pg", "in-progress-pg"} {
		assertRecoveredInterruptedPlayground(ctx, t, st, name)
	}

	running, err := st.GetPlayground(ctx, "running-pg")
	if err != nil {
		t.Fatalf("get running playground: %v", err)
	}
	if running.Status != domain.StatusRunning || running.ErrorMessage != nil {
		t.Fatalf("running playground should be unchanged, got %#v", running)
	}
}

func TestRecoverInterruptedPlaygroundsFailsClosedOnCorruptJSON(t *testing.T) {
	ctx := context.Background()

	assertBadRecoveryJSON := func(t *testing.T, name string, status string, column string, raw string) {
		t.Helper()
		st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })

		pg, err := st.CreatePlayground(ctx, domain.Playground{Name: name, Status: status})
		if err != nil {
			t.Fatalf("create playground: %v", err)
		}
		query := "UPDATE playgrounds SET " + column + "=? WHERE id=?"
		if _, err := st.db.ExecContext(ctx, query, raw, pg.ID); err != nil {
			t.Fatalf("corrupt %s: %v", column, err)
		}
		if _, err := st.RecoverInterruptedPlaygrounds(ctx); err == nil || !strings.Contains(err.Error(), "playgrounds."+column) {
			t.Fatalf("expected %s decode error, got %v", column, err)
		}
	}

	t.Run("creation steps", func(t *testing.T) {
		assertBadRecoveryJSON(t, "bad-steps", domain.StatusPending, "creation_steps_json", "{")
	})
	t.Run("error details", func(t *testing.T) {
		assertBadRecoveryJSON(t, "bad-details", domain.StatusInProgress, "error_details_json", "{")
	})
	t.Run("creation steps null", func(t *testing.T) {
		assertBadRecoveryJSON(t, "null-steps", domain.StatusPending, "creation_steps_json", "null")
	})
	t.Run("error details null", func(t *testing.T) {
		assertBadRecoveryJSON(t, "null-details", domain.StatusInProgress, "error_details_json", "null")
	})
}

func TestRecoverInterruptedPlaygroundsContinuesAfterCorruptJSON(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	for _, pg := range []domain.Playground{
		{Name: "valid-before-corrupt", Status: domain.StatusPending},
		{Name: "corrupt-recovery-row", Status: domain.StatusInProgress},
		{Name: "valid-after-corrupt", Status: domain.StatusPending},
	} {
		if _, err := st.CreatePlayground(ctx, pg); err != nil {
			t.Fatalf("create playground %s: %v", pg.Name, err)
		}
	}
	corrupt, err := st.GetPlayground(ctx, "corrupt-recovery-row")
	if err != nil {
		t.Fatalf("get corrupt playground: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE playgrounds SET error_details_json=? WHERE id=?`, "{", corrupt.ID); err != nil {
		t.Fatalf("corrupt error_details_json: %v", err)
	}

	count, err := st.RecoverInterruptedPlaygrounds(ctx)
	if err == nil || !strings.Contains(err.Error(), "playgrounds.error_details_json") {
		t.Fatalf("expected corrupt JSON error, got count=%d err=%v", count, err)
	}
	if count != 2 {
		t.Fatalf("expected 2 valid rows recovered despite corrupt row, got %d", count)
	}
	assertRecoveredInterruptedPlayground(ctx, t, st, "valid-before-corrupt")
	assertRecoveredInterruptedPlayground(ctx, t, st, "valid-after-corrupt")

	var status string
	if err := st.db.QueryRowContext(ctx, `SELECT status FROM playgrounds WHERE name=?`, "corrupt-recovery-row").Scan(&status); err != nil {
		t.Fatalf("read corrupt row status after recovery: %v", err)
	}
	if status != domain.StatusInProgress {
		t.Fatalf("corrupt row should fail closed without mutation, got status %q", status)
	}
}

func assertRecoveredInterruptedPlayground(ctx context.Context, t *testing.T, st *DB, name string) {
	t.Helper()
	pg, err := st.GetPlayground(ctx, name)
	if err != nil {
		t.Fatalf("get playground %s: %v", name, err)
	}
	if pg.Status != domain.StatusError {
		t.Fatalf("expected %s to be error, got %s", name, pg.Status)
	}
	if pg.StateReason == nil || *pg.StateReason != "lifecycle_interrupted" {
		t.Fatalf("expected lifecycle_interrupted state reason for %s, got %#v", name, pg.StateReason)
	}
	details, ok := pg.ErrorDetails["interrupted"].(map[string]any)
	if !ok || details["code"] != "INTERRUPTED" || details["category"] != "lifecycle_interrupted" {
		t.Fatalf("expected interrupted error details for %s, got %#v", name, pg.ErrorDetails)
	}
	if len(pg.CreationSteps) == 0 || pg.CreationSteps[len(pg.CreationSteps)-1].Status != "error" {
		t.Fatalf("expected error creation step for %s, got %#v", name, pg.CreationSteps)
	}
}

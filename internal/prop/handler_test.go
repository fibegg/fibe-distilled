package prop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

func TestPropUpdateSavesFromCurrentRow(t *testing.T) {
	ctx, st, handler := newTestHandler(t)
	syncedAt := time.Now().UTC().Add(-time.Hour)
	p, err := st.CreateProp(ctx, domain.Prop{
		Name:          "current-row-prop",
		RepositoryURL: "https://example.com/acme/current-row.git",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	current := p
	current.Branches = []string{"main", "feature/a"}
	current.BranchRecords = []domain.PropBranch{
		{Name: "main", Default: true, HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LastSyncedAt: &syncedAt},
		{Name: "feature/a", HeadSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", LastSyncedAt: &syncedAt},
	}
	current.LastSyncedAt = &syncedAt
	if _, err := st.SaveProp(ctx, current); err != nil {
		t.Fatalf("save current prop metadata: %v", err)
	}
	newName := "renamed-current-row-prop"

	updated, err := handler.savePropUpdate(ctx, p.ID, propPayload{Name: &newName, fields: jsonFields{"name": struct{}{}}})
	if err != nil {
		t.Fatalf("save prop update: %v", err)
	}
	if updated.Name != newName {
		t.Fatalf("update should apply requested name, got %#v", updated)
	}
	if len(updated.BranchRecords) != 2 || updated.BranchRecords[1].Name != "feature/a" {
		t.Fatalf("update should preserve current branch metadata, got %#v", updated.BranchRecords)
	}
	if updated.LastSyncedAt == nil {
		t.Fatalf("update should preserve current sync metadata, got %#v", updated)
	}
}

func TestPropRepositoryUpdateClearsStaleSyncMetadata(t *testing.T) {
	ctx, st, handler := newTestHandler(t)
	syncedAt := time.Now().UTC().Add(-time.Hour)
	p, err := st.CreateProp(ctx, domain.Prop{
		Name:          "repointed-prop",
		RepositoryURL: "https://github.com/acme/old.git",
		Provider:      "github",
		Private:       true,
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	current := p
	current.DefaultBranch = "trunk"
	current.Branches = []string{"trunk", "feature/old"}
	current.BranchRecords = []domain.PropBranch{
		{Name: "trunk", Default: true, HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LastSyncedAt: &syncedAt},
		{Name: "feature/old", HeadSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", LastSyncedAt: &syncedAt},
	}
	current.LastSyncedAt = &syncedAt
	if _, err := st.SaveProp(ctx, current); err != nil {
		t.Fatalf("save current prop metadata: %v", err)
	}
	repositoryURL := "ssh://git.example.test/acme/new.git"

	updated, err := handler.savePropUpdate(ctx, p.ID, propPayload{
		RepositoryURL: repositoryURL,
		fields:        jsonFields{"repository_url": struct{}{}},
	})
	if err != nil {
		t.Fatalf("save prop repository update: %v", err)
	}
	if updated.RepositoryURL != repositoryURL || updated.Provider != "git" || updated.Private {
		t.Fatalf("repository update should reset provider/private metadata, got %#v", updated)
	}
	if updated.DefaultBranch != "main" || updated.LastSyncedAt != nil {
		t.Fatalf("repository update should reset default branch and sync timestamp, got %#v", updated)
	}
	if len(updated.BranchRecords) != 1 || updated.BranchRecords[0].Name != "main" || updated.BranchRecords[0].HeadSHA != "" {
		t.Fatalf("repository update should reset stale branch metadata, got %#v", updated.BranchRecords)
	}
}

func TestPropSyncMetadataMergesWithCurrentRow(t *testing.T) {
	fixture := newPropSyncMetadataFixture(t)
	updated, err := fixture.handler.savePropSyncMetadata(fixture.ctx, fixture.source, fixture.synced)
	if err != nil {
		t.Fatalf("save prop sync metadata: %v", err)
	}
	assertSyncedPropMetadata(t, updated)

	repointed := repointedPropForStaleSync(updated)
	if _, err := fixture.store.SaveProp(fixture.ctx, repointed); err != nil {
		t.Fatalf("save repointed prop: %v", err)
	}
	skipped, err := fixture.handler.savePropSyncMetadata(fixture.ctx, fixture.source, fixture.synced)
	if err != nil {
		t.Fatalf("save stale prop sync metadata: %v", err)
	}
	assertStalePropSyncSkipped(t, skipped, repointed)
}

func TestValidatePropPayloadRejectsUnsupportedProviders(t *testing.T) {
	for _, provider := range []string{"gitea", "bitbucket"} {
		t.Run(provider, func(t *testing.T) {
			var payload propPayload
			if err := json.Unmarshal([]byte(`{"repository_url":"ssh://git.example.test/acme/p.git","provider":"`+provider+`"}`), &payload); err != nil {
				t.Fatalf("unmarshal prop payload: %v", err)
			}
			err := validatePropPayload(payload)
			var validation apiValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("expected apiValidationError, got %T %[1]v", err)
			}
			if validation.status != http.StatusNotImplemented || validation.code != "NOT_IMPLEMENTED" {
				t.Fatalf("unexpected validation error: %#v", validation)
			}
		})
	}

	for _, tc := range []struct {
		name          string
		repositoryURL string
		provider      string
		wantErr       bool
	}{
		{name: "generic git", repositoryURL: "ssh://git.example.test/acme/p.git", provider: "git"},
		{name: "github", repositoryURL: "https://github.com/acme/p.git", provider: "github"},
		{name: "generic with github provider", repositoryURL: "ssh://git.example.test/acme/p.git", provider: "github", wantErr: true},
		{name: "github with generic provider", repositoryURL: "https://github.com/acme/p.git", provider: "git", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload propPayload
			if err := json.Unmarshal([]byte(`{"repository_url":"`+tc.repositoryURL+`","provider":"`+tc.provider+`"}`), &payload); err != nil {
				t.Fatalf("unmarshal prop payload: %v", err)
			}
			err := validatePropPayload(payload)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "provider must match repository_url") {
					t.Fatalf("expected provider mismatch error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("supported provider rejected: %v", err)
			}
		})
	}
}

type propSyncMetadataFixture struct {
	ctx     context.Context
	store   *store.DB
	handler Handler
	source  domain.Prop
	synced  domain.Prop
}

func newPropSyncMetadataFixture(t *testing.T) propSyncMetadataFixture {
	t.Helper()
	ctx, st, handler := newTestHandler(t)
	source, err := st.CreateProp(ctx, domain.Prop{
		Name:          "sync-source-prop",
		RepositoryURL: "https://github.com/acme/sync-source.git",
	})
	if err != nil {
		t.Fatalf("create prop: %v", err)
	}
	current := source
	current.Name = "sync-source-renamed"
	if _, err := st.SaveProp(ctx, current); err != nil {
		t.Fatalf("save current prop: %v", err)
	}
	syncedAt := time.Now().UTC()
	synced := source
	synced.DefaultBranch = "trunk"
	synced.Provider = "github"
	synced.Private = true
	synced.Status = "active"
	synced.Branches = []string{"trunk", "feature/b"}
	synced.BranchRecords = []domain.PropBranch{
		{Name: "trunk", Default: true, HeadSHA: "cccccccccccccccccccccccccccccccccccccccc", LastSyncedAt: &syncedAt},
		{Name: "feature/b", HeadSHA: "dddddddddddddddddddddddddddddddddddddddd", LastSyncedAt: &syncedAt},
	}
	synced.LastSyncedAt = &syncedAt
	return propSyncMetadataFixture{ctx: ctx, store: st, handler: handler, source: source, synced: synced}
}

func newTestHandler(t *testing.T) (context.Context, *store.DB, Handler) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return ctx, st, NewHandler(st, Options{})
}

func assertSyncedPropMetadata(t *testing.T, updated domain.Prop) {
	t.Helper()
	if updated.Name != "sync-source-renamed" {
		t.Fatalf("sync should preserve current user-owned name, got %#v", updated)
	}
	if updated.DefaultBranch != "trunk" || len(updated.BranchRecords) != 2 {
		t.Fatalf("sync should update metadata fields, got %#v", updated)
	}
}

func repointedPropForStaleSync(updated domain.Prop) domain.Prop {
	repointed := updated
	repointed.RepositoryURL = "https://github.com/acme/other.git"
	repointed.DefaultBranch = "main"
	repointed.Branches = []string{"main"}
	repointed.BranchRecords = []domain.PropBranch{{Name: "main", Default: true}}
	return repointed
}

func assertStalePropSyncSkipped(t *testing.T, skipped domain.Prop, repointed domain.Prop) {
	t.Helper()
	if skipped.RepositoryURL != repointed.RepositoryURL {
		t.Fatalf("stale sync should return current repointed prop, got %#v", skipped)
	}
	if len(skipped.BranchRecords) != 1 || skipped.BranchRecords[0].Name != "main" {
		t.Fatalf("stale sync should not write old repository branch metadata, got %#v", skipped.BranchRecords)
	}
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/git"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	"github.com/fibegg/fibe-distilled/internal/runtimetest"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"github.com/fibegg/fibe-distilled/internal/worker"
)

func TestRepoStatusUsesProcessGitHubTokenOnly(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	cfg := config.Config{APIToken: "test-token"}
	wk := worker.Worker{DB: st, Runtime: runtime.Checker{Executor: &runtimetest.FakeExecutor{}}}
	srv := httptest.NewServer(New(cfg, st, wk))
	defer srv.Close()

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	res := doReq(t, srv, http.MethodPost, "/api/repo_status_checks", map[string]any{"github_urls": []string{
		"https://github.com/acme/demo",
		"https://www.github.com/acme/demo",
		"acme/demo",
		"acme/demo@feature/ref",
		"acme/demo.git/",
		"git@github.com:acme/demo.git",
		"git@github.com:acme/demo.git/",
		"git@www.github.com:acme/demo.git",
		"git@www.github.com:acme/demo.git/",
	}}, "test-token")
	var body map[string]any
	decodeResp(t, res, &body)
	repos := body["repos"].([]any)
	if len(repos) != 9 {
		t.Fatalf("expected nine repo status entries, got %#v", body)
	}
	for _, raw := range repos {
		entry := raw.(map[string]any)
		if entry["status"] != "not_writable" || entry["error"] != "GitHub token is missing" {
			t.Fatalf("repo status should use only the process token, got %#v", entry)
		}
		if entry["github_url"] != "https://github.com/acme/demo" {
			t.Fatalf("repo status should expose canonical github_url, got %#v", entry)
		}
	}
}

func TestRepoStatusDoesNotLoadGitHubTokenForGenericGitURLs(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	cfg := config.Config{APIToken: "test-token", GitHubTok: "fallback-token"}
	wk := worker.Worker{DB: st, Runtime: runtime.Checker{Executor: &runtimetest.FakeExecutor{}}}
	srv := httptest.NewServer(New(cfg, st, wk))
	defer srv.Close()

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	var body map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/repo_status_checks", map[string]any{"github_urls": []string{
		"ssh://git.example.test/acme/demo.git",
		"git@git.example.test:acme/demo.git",
		"https://notgithub.com/acme/demo.git",
		"https://github.com.evil/acme/demo.git",
		"git@github.com.evil:acme/demo.git",
	}}, "test-token")
	decodeResp(t, res, &body)
	repos := body["repos"].([]any)
	if len(repos) != 5 {
		t.Fatalf("expected five repo status entries: %#v", body)
	}
	for _, raw := range repos {
		entry := raw.(map[string]any)
		if entry["authenticated"] != false || entry["status"] != "ready" {
			t.Fatalf("expected generic Git repo status without GitHub auth dependency: %#v", entry)
		}
		for _, field := range []string{"github_url", "runtime_writable"} {
			if _, exists := entry[field]; exists {
				t.Fatalf("generic Git repo should not expose %s: %#v", field, entry)
			}
		}
	}
}

func TestRepoStatusRejectsEmptyURLLists(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "missing", body: map[string]any{}},
		{name: "null", body: map[string]any{"github_urls": nil}},
		{name: "empty", body: map[string]any{"github_urls": []string{}}},
		{name: "blank entry", body: map[string]any{"github_urls": []string{" "}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res := doReq(t, srv, http.MethodPost, "/api/repo_status_checks", tt.body, "test-token")
			assertBadRequest(t, res, "bad repo status body")
		})
	}
}

func TestRepoStatusRejectsMalformedGitHubRepoInputs(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	for _, rawURL := range []string{
		"http://github.com/acme/demo.git",
		"git://github.com/acme/demo.git",
		"ftp://github.com/acme/demo.git",
		"git+ssh://github.com/acme/demo.git",
		"https://github.com/acme/demo.git?tab=readme",
		"https://github.com/acme/demo.git#readme",
		"ssh://git@github.com/acme/demo.git?depth=1",
		"https://github.com/acme/demo/extra",
		"https://www.github.com/acme/demo/extra",
		"ssh://git@github.com/acme/demo/extra.git",
		"git@github.com:acme/demo/extra.git",
		"git@www.github.com:acme/demo/extra.git",
		"acme/demo/extra",
		"file:///tmp/acme/demo.git",
		"ftp://git.example.test/acme/demo.git",
		"foo://git.example.test/acme/demo.git",
		"git+ssh://git.example.test/acme/demo.git",
	} {
		t.Run(rawURL, func(t *testing.T) {
			var body map[string]any
			res := doReq(t, srv, http.MethodPost, "/api/repo_status_checks", map[string]any{"github_urls": []string{rawURL}}, "test-token")
			decodeResp(t, res, &body)
			repos := body["repos"].([]any)
			entry := repos[0].(map[string]any)
			if entry["status"] != "invalid" || entry["error"] != "invalid URL" {
				t.Fatalf("expected invalid repo status, got %#v", entry)
			}
		})
	}
}

func TestSameRepositoryURLNormalizesGitSuffixBeforeComparison(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		left  string
		right string
	}{
		{
			name:  "https generic trailing git slash",
			left:  "https://git.example.test/acme/demo.git/",
			right: "https://git.example.test/acme/demo",
		},
		{
			name:  "scp generic trailing git slash",
			left:  "git@git.example.test:acme/demo.git/",
			right: "git@git.example.test:acme/demo",
		},
		{
			name:  "scp github trailing git slash",
			left:  "git@github.com:acme/demo.git/",
			right: "https://github.com/acme/demo",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !git.SameRepositoryURL(tt.left, tt.right) {
				t.Fatalf("expected %q and %q to match", tt.left, tt.right)
			}
		})
	}

	if git.SameRepositoryURL("https://git.example.test/acme/demo.git/", "https://git.example.test/acme/other") {
		t.Fatal("different generic repositories should not match")
	}
}

func TestScopedDeletionLocks(t *testing.T) {
	srv, st := newTestServerWithStore(t, nil)
	defer srv.Close()
	ensureTestConfiguredMarquee(t, st)

	propBody := map[string]any{"prop": map[string]any{
		"name":           "demo-prop",
		"repository_url": "https://example.com/acme/demo.git",
	}}
	var prop map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/props", propBody, "test-token")
	decodeResp(t, res, &prop)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "locked-spec",
		"base_compose_yaml": `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://example.com/acme/demo.git
      fibe.gg/source_mount: /app
`,
	}}
	var playspec map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)

	res = doReq(t, srv, http.MethodDelete, "/api/props/demo-prop", nil, "test-token")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("expected prop delete conflict, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)

	pgBody := map[string]any{"playground": map[string]any{
		"name":        "locked-pg",
		"playspec_id": int64(playspec["id"].(float64)),
	}}
	var pg map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playgrounds", pgBody, "test-token")
	decodeResp(t, res, &pg)

	res = doReq(t, srv, http.MethodDelete, "/api/playspecs/locked-spec", nil, "test-token")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("expected playspec delete conflict, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)

	res = doReq(t, srv, http.MethodGet, "/api/playspecs/locked-spec", nil, "test-token")
	decodeResp(t, res, &playspec)
	if playspec["locked"] != true || playspec["playground_count"].(float64) != 1 {
		t.Fatalf("expected locked playspec metadata, got %#v", playspec)
	}

	marquee := ensureTestConfiguredMarquee(t, st)
	updateBody := map[string]any{"playground": map[string]any{"marquee_id": marquee.ID}}
	res = doReq(t, srv, http.MethodPatch, "/api/playgrounds/locked-pg", updateBody, "test-token")
	decodeResp(t, res, &pg)

	res = doReq(t, srv, http.MethodDelete, "/api/marquees/"+store.ConfiguredMarqueeName, nil, "test-token")
	assertErrorCode(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestPropUpdateRepositoryURLRequiresWritableGitHubAccess(t *testing.T) {
	githubBaseURL := startNonWritableGitHubRepoFixture(t, "acme/private-demo")

	srv := newTestServerWithGitHubBaseURL(t, githubBaseURL)
	defer srv.Close()

	propBody := map[string]any{"prop": map[string]any{
		"name":           "update-repo-access",
		"repository_url": "ssh://git.example.test/acme/private-demo.git",
	}}
	var prop map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/props", propBody, "test-token")
	decodeResp(t, res, &prop)

	res = doReq(t, srv, http.MethodPatch, "/api/props/update-repo-access", map[string]any{"prop": map[string]any{
		"repository_url": "https://github.com/acme/private-demo.git",
	}}, "test-token")
	if res.StatusCode != http.StatusUnprocessableEntity {
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		t.Fatalf("expected repository access failure, got %d: %#v", res.StatusCode, got)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	closeResponseBody(t, res)
	errObj := got["error"].(map[string]any)
	if errObj["code"] != "REPOSITORY_REQUIRES_FORK" {
		t.Fatalf("unexpected error: %#v", got)
	}

	res = doReq(t, srv, http.MethodGet, "/api/props/update-repo-access", nil, "test-token")
	decodeResp(t, res, &prop)
	if prop["repository_url"] != "ssh://git.example.test/acme/private-demo.git" {
		t.Fatalf("rejected update should not change prop repository: %#v", prop)
	}
}

func TestRuntimeRepositoryURLsRejectUnsafeOrNonCloneableInputs(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	for _, raw := range []string{"acme/demo", "../demo", "acme/..", "https://github.com/acme/demo/extra", "https://github.com/../demo.git", "foo://git.example.test/acme/demo.git", "https://git.example.test", "ssh://git.example.test", "git://git.example.test/", "git@git.example.test:/", "git@git.example.test:////", "git@git.example.test:../demo.git", "git@git.example.test:acme/../demo.git", "https://git.example.test/acme/../demo.git", "https://git.example.test/acme/%2e%2e/demo.git", "https://github.com/acme/demo.git?tab=readme", "ssh://git.example.test/acme/demo.git#readme"} {
		res := doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
			"name":           "bad-repository-prop",
			"repository_url": raw,
		}}, "test-token")
		assertBadRequest(t, res, "Prop repository "+raw)

		res = doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
			"name":           "bad-repository-launch",
			"repository_url": raw,
			"compose_yaml": `services:
  web:
    image: alpine
`,
		}, "test-token")
		assertBadRequest(t, res, "Launch repository "+raw)
	}

	credentialed := "https://x-access-token:ghp_secret@github.com/acme/demo.git"
	res := doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           "credentialed-prop",
		"repository_url": credentialed,
	}}, "test-token")
	assertBadRequestWithoutSecret(t, res)

	res = doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name":           "credentialed-launch",
		"repository_url": credentialed,
		"compose_yaml": `services:
  web:
    image: alpine
`,
	}, "test-token")
	assertBadRequestWithoutSecret(t, res)

	res = doReq(t, srv, http.MethodPost, "/api/launches", map[string]any{
		"name": "credentialed-compose-launch",
		"compose_yaml": `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://x-access-token:ghp_secret@github.com/acme/demo.git
`,
	}, "test-token")
	assertBadRequestWithoutSecret(t, res)

	var prop map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           "safe-ssh-prop",
		"repository_url": "ssh://git@git.example.test/acme/demo.git",
	}}, "test-token")
	decodeResp(t, res, &prop)
	if prop["repository_url"] != "ssh://git@git.example.test/acme/demo.git" {
		t.Fatalf("ssh username-only URL should be allowed, got %#v", prop)
	}

	res = doReq(t, srv, http.MethodPatch, "/api/props/safe-ssh-prop", map[string]any{"prop": map[string]any{
		"repository_url": credentialed,
	}}, "test-token")
	assertBadRequestWithoutSecret(t, res)

	var status map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/repo_status_checks", map[string]any{"github_urls": []string{credentialed}}, "test-token")
	decodeResp(t, res, &status)
	text := fmt.Sprint(status)
	if strings.Contains(text, "ghp_secret") {
		t.Fatalf("repo status response leaked credential: %#v", status)
	}
	repos := status["repos"].([]any)
	if len(repos) != 1 {
		t.Fatalf("expected one repo status: %#v", status)
	}
	entry := repos[0].(map[string]any)
	if entry["status"] != "invalid" || !strings.Contains(fmt.Sprint(entry["url"]), "https://***@github.com/acme/demo.git") {
		t.Fatalf("expected redacted invalid credentialed URL, got %#v", entry)
	}
}

func TestNonGitHubPropCreateDoesNotRequireGitHubToken(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	wk := worker.Worker{DB: st, Runtime: runtime.Checker{Executor: &runtimetest.FakeExecutor{}}}
	srv := httptest.NewServer(New(config.Config{APIToken: "test-token"}, st, wk))
	defer srv.Close()

	var prop map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           "ssh-only-prop",
		"repository_url": "ssh://git.example.test/acme/ssh-only.git",
	}}, "test-token")
	decodeResp(t, res, &prop)
	if prop["repository_url"] != "ssh://git.example.test/acme/ssh-only.git" {
		t.Fatalf("unexpected prop: %#v", prop)
	}
}

func TestNonGitHubPropSyncDoesNotFabricateBranchEvidence(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fibe-distilled.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	wk := worker.Worker{DB: st, Runtime: runtime.Checker{Executor: &runtimetest.FakeExecutor{}}}
	srv := httptest.NewServer(New(config.Config{APIToken: "test-token"}, st, wk))
	defer srv.Close()

	var created map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           "generic-sync-prop",
		"repository_url": "ssh://git.example.test/acme/generic-sync.git",
	}}, "test-token")
	decodeResp(t, res, &created)

	var prop map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/props/generic-sync-prop/syncs", nil, "test-token")
	decodeResp(t, res, &prop)
	if prop["updated_at"] != created["updated_at"] {
		t.Fatalf("generic sync should not write the Prop row, got updated_at=%v want %v", prop["updated_at"], created["updated_at"])
	}
	if prop["last_synced_at"] != nil {
		t.Fatalf("generic sync should not stamp last_synced_at without remote metadata: %#v", prop)
	}

	items := propBranches(t, srv, "generic-sync-prop", "")
	if len(items) != 1 {
		t.Fatalf("generic sync should preserve only stored branch names, got %#v", items)
	}
	branch := items[0].(map[string]any)
	if branch["name"] != "main" || branch["head_sha"] != nil || branch["last_synced_at"] != nil {
		t.Fatalf("generic sync should not fabricate branch evidence: %#v", branch)
	}
}

func TestPropCreateDoesNotExposeEnvDefaults(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	var prop map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           "prop-cache-shape",
		"repository_url": "https://example.com/acme/cache-shape.git",
	}}, "test-token")
	decodeResp(t, res, &prop)
	if _, ok := prop["env_defaults"]; ok {
		t.Fatalf("prop response should not expose removed env defaults: %#v", prop)
	}
	if _, ok := prop["has_credentials"]; ok {
		t.Fatalf("prop response should not expose removed credential hints: %#v", prop)
	}
	if _, ok := prop["original_repository_url"]; ok {
		t.Fatalf("prop response should not expose removed credential URL hints: %#v", prop)
	}
}

func TestPropSyncsGitHubBranchesAndPlayspecServiceMetadata(t *testing.T) {
	githubBaseURL := startPropSyncGitHubFixture(t)

	srv := newTestServerWithGitHubBaseURL(t, githubBaseURL)
	defer srv.Close()

	propBody := map[string]any{"prop": map[string]any{
		"name":           "demo-prop",
		"repository_url": "https://github.com/acme/demo.git",
	}}
	var prop map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/props", propBody, "test-token")
	decodeResp(t, res, &prop)

	playspecBody := map[string]any{"playspec": map[string]any{
		"name": "branch-service-spec",
		"base_compose_yaml": `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
      fibe.gg/branch: feature/z
    volumes:
      - ${FIBE_SERVICES_WEB_PATH}:/app
`,
	}}
	var playspec map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/playspecs", playspecBody, "test-token")
	decodeResp(t, res, &playspec)
	service := playspec["services"].([]any)[0].(map[string]any)
	if service["repo_url"] != "https://github.com/acme/demo.git" {
		t.Fatalf("playspec service should expose repository metadata, got %#v", service)
	}

	items := propBranches(t, srv, "demo-prop", "")
	if len(items) != 1 {
		t.Fatalf("unsynced prop should expose only its stored default branch, got %#v", items)
	}
	branch := items[0].(map[string]any)
	if branch["name"] != "main" || branch["default"] != true {
		t.Fatalf("unexpected unsynced branch metadata: %#v", branch)
	}
	if branch["head_sha"] != nil || branch["last_synced_at"] != nil {
		t.Fatalf("unsynced branch should not include GitHub metadata: %#v", branch)
	}

	updateBody := map[string]any{"playspec": map[string]any{
		"base_compose_yaml": `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
      fibe.gg/branch: bugfix/a
    volumes:
      - ${FIBE_SERVICES_WEB_PATH}:/app
`,
	}}
	res = doReq(t, srv, http.MethodPatch, "/api/playspecs/branch-service-spec", updateBody, "test-token")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("playspec update should not require Prop sync, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)

	res = doReq(t, srv, http.MethodPost, "/api/props/demo-prop/syncs", nil, "test-token")
	decodeResp(t, res, &prop)
	if prop["default_branch"] != "trunk" || prop["provider"] != "github" {
		t.Fatalf("unexpected synced prop: %#v", prop)
	}

	items = propBranches(t, srv, "demo-prop", "")
	if len(items) != 3 {
		t.Fatalf("expected three branches, got %#v", items)
	}
	branch = items[0].(map[string]any)
	if branch["name"] != "trunk" || branch["default"] != true {
		t.Fatalf("default branch should be first, got %#v", items)
	}
	if branch["head_sha"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || branch["last_synced_at"] == "" {
		t.Fatalf("default branch metadata was not persisted: %#v", branch)
	}
	if branch["env_files"] != nil || branch["env_file_errors"] != nil || branch["sync_error"] != nil {
		t.Fatalf("branch response should not expose removed env-default metadata: %#v", branch)
	}

	items = propBranches(t, srv, "demo-prop", "query=feature&limit=1")
	if len(items) != 1 || items[0].(map[string]any)["name"] != "feature/z" {
		t.Fatalf("unexpected filtered branches: %#v", items)
	}
	branch = items[0].(map[string]any)
	if branch["head_sha"] != "fffffff111111111111111111111111111111111" {
		t.Fatalf("filtered branch should include its head SHA: %#v", branch)
	}
	if branch["env_files"] != nil || branch["env_file_errors"] != nil || branch["sync_error"] != nil {
		t.Fatalf("branch response should not expose removed env-default metadata: %#v", branch)
	}
	assertBadPropBranchLimits(t, srv, "demo-prop")

	items = propBranches(t, srv, "demo-prop", "query=bugfix")
	if len(items) != 1 || items[0].(map[string]any)["name"] != "bugfix/a" {
		t.Fatalf("unexpected bugfix branch result: %#v", items)
	}
	branch = items[0].(map[string]any)
	if branch["head_sha"] != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("bugfix branch should include its head SHA: %#v", branch)
	}
}

func TestPropsEnvDefaultsUnsupported(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	propBody := map[string]any{"prop": map[string]any{
		"name":           "unsafe-env-prop",
		"repository_url": "https://example.com/acme/demo.git",
	}}
	var prop map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/props", propBody, "test-token")
	decodeResp(t, res, &prop)

	res = doReq(t, srv, http.MethodGet, "/api/props/unsafe-env-prop/env_defaults?env_file_path=.env.example", nil, "test-token")
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("env defaults should be unsupported, got %d", res.StatusCode)
	}
	assertErrorMessageContains(t, res, http.StatusNotImplemented, "NOT_IMPLEMENTED", "does not fetch env files")
}

func TestPropCRUDByNameAfterRename(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	const name = "crud-prop"
	const updated = name + "-renamed"

	rejected := doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           "excluded-provider-prop",
		"repository_url": "ssh://git.example.test/fibe-admin/excluded-provider-prop.git",
		"provider":       "gitea",
		"default_branch": "main",
	}}, "test-token")
	if rejected.StatusCode != http.StatusNotImplemented {
		t.Fatalf("excluded provider should return 501, got %d", rejected.StatusCode)
	}
	closeResponseBody(t, rejected)

	rejected = doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           "unknown-provider-prop",
		"repository_url": "ssh://git.example.test/fibe-admin/unknown-provider-prop.git",
		"provider":       "bitbucket",
		"default_branch": "main",
	}}, "test-token")
	if rejected.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unknown provider should return 501, got %d", rejected.StatusCode)
	}
	closeResponseBody(t, rejected)

	var genericProviderProp map[string]any
	res := doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           " generic-provider-prop ",
		"repository_url": " ssh://git.example.test/fibe-admin/generic-provider-prop.git ",
		"provider":       " Git ",
		"default_branch": " main ",
	}}, "test-token")
	assertStatus(t, res, http.StatusCreated, "provider=git prop create should be allowed")
	decodeResp(t, res, &genericProviderProp)
	if genericProviderProp["provider"] != "git" {
		t.Fatalf("explicit generic Git provider should persist, got %#v", genericProviderProp)
	}
	if genericProviderProp["name"] != "generic-provider-prop" || genericProviderProp["default_branch"] != "main" {
		t.Fatalf("prop text fields should trim whitespace, got %#v", genericProviderProp)
	}
	if genericProviderProp["repository_url"] != "ssh://git.example.test/fibe-admin/generic-provider-prop.git" {
		t.Fatalf("repository_url should trim transport whitespace, got %#v", genericProviderProp)
	}

	assertListNames(t, srv, "/api/props?provider=%20Git%20", []string{"generic-provider-prop"})

	res = doReq(t, srv, http.MethodPatch, "/api/props/generic-provider-prop", map[string]any{"prop": map[string]any{"provider": "bitbucket"}}, "test-token")
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unknown provider update should return 501, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)

	res = doReq(t, srv, http.MethodPatch, "/api/props/generic-provider-prop", map[string]any{"prop": map[string]any{"provider": " GitHub "}}, "test-token")
	assertBadRequest(t, res, "provider must match repository_url")

	res = doReq(t, srv, http.MethodGet, "/api/props/generic-provider-prop", nil, "test-token")
	decodeResp(t, res, &genericProviderProp)
	if genericProviderProp["provider"] != "git" {
		t.Fatalf("rejected provider update should not mutate prop, got %#v", genericProviderProp)
	}

	res = doReq(t, srv, http.MethodPatch, "/api/props/generic-provider-prop", map[string]any{"prop": map[string]any{"repository_url": " ssh://git.example.test/fibe-admin/generic-provider-prop-renamed.git "}}, "test-token")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("repository_url whitespace update should be allowed, got %d", res.StatusCode)
	}
	decodeResp(t, res, &genericProviderProp)
	if genericProviderProp["repository_url"] != "ssh://git.example.test/fibe-admin/generic-provider-prop-renamed.git" {
		t.Fatalf("repository_url update should trim whitespace, got %#v", genericProviderProp)
	}

	var created map[string]any
	res = doReq(t, srv, http.MethodPost, "/api/props", map[string]any{"prop": map[string]any{
		"name":           name,
		"repository_url": "ssh://git.example.test/fibe-admin/crud-prop.git",
		"default_branch": "main",
	}}, "test-token")
	assertStatus(t, res, http.StatusCreated, "generic prop create should be allowed")
	decodeResp(t, res, &created)
	createdID := created["id"]

	var shown map[string]any
	res = doReq(t, srv, http.MethodGet, "/api/props/"+name, nil, "test-token")
	decodeResp(t, res, &shown)
	if shown["id"] != createdID {
		t.Fatalf("get by name should resolve the created prop: %#v", shown)
	}

	var renamed map[string]any
	res = doReq(t, srv, http.MethodPatch, "/api/props/"+name, map[string]any{"prop": map[string]any{"name": updated}}, "test-token")
	decodeResp(t, res, &renamed)
	if renamed["id"] != createdID || renamed["name"] != updated {
		t.Fatalf("rename should keep id and update name: %#v", renamed)
	}

	res = doReq(t, srv, http.MethodGet, "/api/props/"+updated, nil, "test-token")
	decodeResp(t, res, &renamed)
	if renamed["id"] != createdID {
		t.Fatalf("get by renamed name should resolve the same prop: %#v", renamed)
	}

	res = doReq(t, srv, http.MethodDelete, "/api/props/"+updated, nil, "test-token")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete by renamed name expected 204, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)

	res = doReq(t, srv, http.MethodGet, "/api/props/"+updated, nil, "test-token")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete expected 404, got %d", res.StatusCode)
	}
	closeResponseBody(t, res)
}

package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestNewUsesBoundedHTTPClient(t *testing.T) {
	client, err := New("", "")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if got, want := client.client.Client().Timeout, 30*time.Second; got != want {
		t.Fatalf("HTTP timeout = %s, want %s", got, want)
	}
}

func TestBranchesPaginatesAllGitHubPages(t *testing.T) {
	var requestedPages []string
	server := httptest.NewServer(branchPaginationHandler(t, &requestedPages))
	defer server.Close()

	client, err := New(server.URL, "")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	branches, err := client.Branches(context.Background(), "acme/demo")
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	assertBranches(t, branches, []Branch{{Name: "main", SHA: "sha-main"}, {Name: "release", SHA: "sha-release"}})
	assertStrings(t, requestedPages, []string{"1", "2"})
}

func TestNewTrimsAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer ghp_token"; got != want {
			t.Errorf("Authorization header = %q, want %q", got, want)
		}
		if r.URL.Path != "/repos/acme/demo" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default_branch": "main",
			"private":        true,
			"permissions":    map[string]bool{"push": true},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, " ghp_token\n")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	repo, err := client.Repository(context.Background(), "acme/demo")
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	if repo.DefaultBranch != "main" || !repo.Private || !repo.Permissions["push"] {
		t.Fatalf("unexpected repository metadata: %#v", repo)
	}
}

type branchPayload struct {
	Name   string        `json:"name"`
	Commit commitPayload `json:"commit"`
}

type commitPayload struct {
	SHA string `json:"sha"`
}

func writeBranches(t *testing.T, w http.ResponseWriter, branches []branchPayload) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(branches); err != nil {
		t.Fatalf("encode branches: %v", err)
	}
}

func branchPaginationHandler(t *testing.T, requestedPages *[]string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/demo/branches" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		page := branchPage(r)
		*requestedPages = append(*requestedPages, page)
		writeBranchPage(t, w, r, page)
	})
}

func writeBranchPage(t *testing.T, w http.ResponseWriter, r *http.Request, page string) {
	t.Helper()
	switch page {
	case "1":
		w.Header().Set("Link", `<`+serverURL(r)+`/repos/acme/demo/branches?page=2>; rel="next"`)
		writeBranches(t, w, []branchPayload{{Name: "main", Commit: commitPayload{SHA: "sha-main"}}})
	case "2":
		writeBranches(t, w, []branchPayload{{Name: "release", Commit: commitPayload{SHA: "sha-release"}}})
	default:
		t.Errorf("unexpected page: %s", page)
		http.NotFound(w, r)
	}
}

func branchPage(r *http.Request) string {
	page := r.URL.Query().Get("page")
	if page == "" {
		return "1"
	}
	return page
}

func assertBranches(t *testing.T, got []Branch, want []Branch) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("branches = %#v, want %#v", got, want)
	}
}

func assertStrings(t *testing.T, got []string, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strings = %#v, want %#v", got, want)
	}
}

func serverURL(r *http.Request) string {
	if r.TLS != nil {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestDownloadRemoteDirSkipsSymlinks(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	if err := os.MkdirAll(filepath.Join(remote, ".venv", "bin"), 0o755); err != nil {
		t.Fatalf("create remote tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(remote, "Gemfile"), []byte("source 'https://rubygems.org'\n"), 0o644); err != nil {
		t.Fatalf("write remote file: %v", err)
	}
	if err := os.Symlink("/missing/container-python", filepath.Join(remote, ".venv", "bin", "python3")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	local := filepath.Join(t.TempDir(), "local")
	if err := downloadRemoteDir(context.Background(), LocalFS{}, domain.Marquee{}, remote, local); err != nil {
		t.Fatalf("download remote dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "Gemfile")); err != nil {
		t.Fatalf("expected regular file to be mirrored: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(local, ".venv", "bin", "python3")); !os.IsNotExist(err) {
		t.Fatalf("expected symlink to be skipped, got err=%v", err)
	}
}

func TestUpdateGitCheckoutPreservesIgnoredRuntimeFiles(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	repo, err := git.PlainInit(remote, false)
	if err != nil {
		t.Fatalf("init remote repo: %v", err)
	}
	writeFile(t, filepath.Join(remote, ".gitignore"), "tmp/cache/\n.venv/\n")
	writeFile(t, filepath.Join(remote, "app.txt"), "old\n")
	commitAll(t, repo, "initial")

	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := cloneGitCheckout(checkout, GitSyncRequest{RepoURL: remote, Branch: "master"}); err != nil {
		t.Fatalf("clone checkout: %v", err)
	}
	cacheFile := filepath.Join(checkout, "tmp", "cache", "bootsnap", "compile-cache-iseq", "entry")
	writeFile(t, cacheFile, "runtime cache\n")
	writeFile(t, filepath.Join(checkout, ".venv", "bin", "python3"), "runtime python shim\n")

	writeFile(t, filepath.Join(remote, "app.txt"), "new\n")
	commitAll(t, repo, "update")

	if err := updateGitCheckout(checkout, GitSyncRequest{RepoURL: remote, Branch: "master"}); err != nil {
		t.Fatalf("update checkout: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(checkout, "app.txt")); err != nil || string(got) != "new\n" {
		t.Fatalf("expected tracked file update, got %q err=%v", got, err)
	}
	if got, err := os.ReadFile(cacheFile); err != nil || string(got) != "runtime cache\n" {
		t.Fatalf("expected ignored runtime cache to remain, got %q err=%v", got, err)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func commitAll(t *testing.T, repo *git.Repository, message string) {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatalf("add files: %v", err)
	}
	_, err = wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "fibe-distilled-test",
			Email: "test@example.invalid",
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("commit %q: %v", message, err)
	}
}

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
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

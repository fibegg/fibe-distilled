package runtime

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// LocalFS implements RemoteFS against the local host filesystem.
type LocalFS struct{}

// MkdirAll creates a local runtime directory tree.
func (f LocalFS) MkdirAll(ctx context.Context, _ domain.Marquee, remotePath string, perm os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(remotePath, perm); err != nil {
		return err
	}
	return os.Chmod(remotePath, perm)
}

// WriteRemoteFile writes a local runtime file with the requested permissions.
func (f LocalFS) WriteRemoteFile(ctx context.Context, _ domain.Marquee, remotePath string, content []byte, perm os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// #nosec G703 -- RemoteFS callers pass fibe-distilled runtime paths produced by optfibe/path validators.
	if err := os.MkdirAll(filepath.Dir(remotePath), 0o700); err != nil {
		return err
	}
	// #nosec G703 -- RemoteFS callers pass fibe-distilled runtime paths produced by optfibe/path validators.
	if err := os.WriteFile(remotePath, content, perm); err != nil {
		return err
	}
	// #nosec G703 -- RemoteFS callers pass fibe-distilled runtime paths produced by optfibe/path validators.
	return os.Chmod(remotePath, perm)
}

// ReadRemoteFile reads one local runtime file.
func (f LocalFS) ReadRemoteFile(ctx context.Context, _ domain.Marquee, remotePath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// #nosec G304 -- RemoteFS callers pass fibe-distilled runtime paths produced by optfibe/path validators.
	content, err := os.ReadFile(remotePath)
	if err != nil {
		return nil, runtimeFileError(err)
	}
	return content, nil
}

// RemoveAll removes a local runtime path tree.
func (f LocalFS) RemoveAll(ctx context.Context, _ domain.Marquee, remotePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.RemoveAll(remotePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Rename renames a local runtime path.
func (f LocalFS) Rename(ctx context.Context, _ domain.Marquee, oldPath string, newPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(oldPath, newPath)
}

// Chmod changes local runtime path permissions.
func (f LocalFS) Chmod(ctx context.Context, _ domain.Marquee, remotePath string, perm os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return runtimeFileError(os.Chmod(remotePath, perm))
}

// Stat returns local runtime file metadata.
func (f LocalFS) Stat(ctx context.Context, _ domain.Marquee, remotePath string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(remotePath)
	return info, runtimeFileError(err)
}

// ReadDir lists a local runtime directory.
func (f LocalFS) ReadDir(ctx context.Context, _ domain.Marquee, remotePath string) ([]fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(remotePath)
	if err != nil {
		return nil, runtimeFileError(err)
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// runtimeFileError normalizes missing runtime files to ErrRemoteFileMissing.
func runtimeFileError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
		return ErrRemoteFileMissing
	}
	return err
}

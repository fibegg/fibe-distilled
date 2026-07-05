package runtime

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

// ListPlaygroundProjects returns safe project directories under the managed Playground root.
func (c Checker) ListPlaygroundProjects(ctx context.Context, marquee domain.Marquee) ([]string, error) {
	ctx, cancel := withRuntimeTimeout(ctx, 30*time.Second)
	defer cancel()

	entries, err := c.playgroundDirectoryEntries(ctx, marquee)
	if err != nil {
		return nil, err
	}
	return safeProjectNames(entries), nil
}

// playgroundDirectoryEntries lists the runtime Playground root.
func (c Checker) playgroundDirectoryEntries(ctx context.Context, marquee domain.Marquee) ([]fs.FileInfo, error) {
	entries, err := c.remoteFS().ReadDir(ctx, marquee, optfibe.PlaygroundsPath)
	if errors.Is(err, ErrRemoteFileMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// safeProjectNames filters runtime entries to validated Compose project names.
func safeProjectNames(entries []fs.FileInfo) []string {
	projects := make([]string, 0, len(entries))
	for _, entry := range entries {
		if project, ok := safeProjectName(entry); ok {
			projects = append(projects, project)
		}
	}
	return projects
}

// safeProjectName returns one managed project directory name.
func safeProjectName(entry fs.FileInfo) (string, bool) {
	if entry == nil || !entry.IsDir() {
		return "", false
	}
	project := strings.TrimSpace(entry.Name())
	if _, err := playgroundBase(project); err != nil {
		return "", false
	}
	return project, true
}

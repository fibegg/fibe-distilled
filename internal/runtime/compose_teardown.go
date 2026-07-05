package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// DestroyCompose tears down a project and removes its Compose volumes and files.
func (c Checker) DestroyCompose(ctx context.Context, marquee domain.Marquee, project string) error {
	return c.teardownCompose(ctx, marquee, project, true)
}

// DownCompose tears down a project while preserving its files and named volumes.
func (c Checker) DownCompose(ctx context.Context, marquee domain.Marquee, project string) error {
	return c.teardownCompose(ctx, marquee, project, false)
}

// teardownCompose tears down a project through typed Compose, Docker, and local filesystem adapters.
func (c Checker) teardownCompose(ctx context.Context, marquee domain.Marquee, project string, destroyFiles bool) error {
	ctx, cancel := withRuntimeTimeout(ctx, 2*time.Minute)
	defer cancel()
	base, err := playgroundBase(project)
	if err != nil {
		return err
	}
	if err := c.downExistingCompose(ctx, marquee, project, base, destroyFiles); err != nil {
		return err
	}
	if err := c.dockerRuntime().CleanupProject(ctx, marquee, project, destroyFiles); err != nil {
		return fmt.Errorf("docker cleanup failed: %w", err)
	}
	if destroyFiles {
		if err := c.remoteFS().RemoveAll(ctx, marquee, base); err != nil {
			return fmt.Errorf("remove playground files failed: %w", err)
		}
	}
	return nil
}

// downExistingCompose runs compose down only when a runtime compose file exists.
func (c Checker) downExistingCompose(ctx context.Context, marquee domain.Marquee, project string, base string, destroyFiles bool) error {
	content, readErr := c.remoteFS().ReadRemoteFile(ctx, marquee, base+"/compose.yml")
	if readErr != nil && !errors.Is(readErr, ErrRemoteFileMissing) {
		return fmt.Errorf("read compose.yml failed: %w", readErr)
	}
	if readErr == nil {
		if err := c.composeRuntime().Down(ctx, marquee, project, base, string(content), destroyFiles); err != nil {
			return fmt.Errorf("compose teardown failed: %w", err)
		}
	}
	return nil
}

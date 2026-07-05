package runtime

import (
	"context"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// SourceDirtyPaths returns synced source checkout paths with local-change evidence.
func (c Checker) SourceDirtyPaths(ctx context.Context, marquee domain.Marquee, project string, paths []string) ([]string, error) {
	if _, err := playgroundBase(project); err != nil {
		return nil, err
	}
	return c.gitRuntime().DirtyPaths(ctx, marquee, project, paths)
}

package runtime

import (
	"context"
	"fmt"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// StartCompose starts a Compose project.
func (c Checker) StartCompose(ctx context.Context, marquee domain.Marquee, project string) error {
	return c.runComposeAction(ctx, marquee, project, "start", c.composeRuntime().Start)
}

// StopCompose stops a Compose project.
func (c Checker) StopCompose(ctx context.Context, marquee domain.Marquee, project string) error {
	return c.runComposeAction(ctx, marquee, project, "stop", c.composeRuntime().Stop)
}

// runComposeAction loads compose.yml and runs one typed Compose action.
func (c Checker) runComposeAction(
	ctx context.Context,
	marquee domain.Marquee,
	project string,
	action string,
	run func(context.Context, domain.Marquee, string, string, string) error,
) error {
	base, composeYAML, err := c.projectComposeYAML(ctx, marquee, project)
	if err != nil {
		return err
	}
	if err := run(ctx, marquee, project, base, composeYAML); err != nil {
		return fmt.Errorf("docker compose %s failed: %w", action, err)
	}
	return nil
}

// projectComposeYAML loads compose.yml for an existing project.
func (c Checker) projectComposeYAML(ctx context.Context, marquee domain.Marquee, project string) (string, string, error) {
	base, err := playgroundBase(project)
	if err != nil {
		return "", "", err
	}
	content, err := c.remoteFS().ReadRemoteFile(ctx, marquee, base+"/compose.yml")
	if err != nil {
		return "", "", fmt.Errorf("read compose.yml failed: %w", err)
	}
	return base, string(content), nil
}

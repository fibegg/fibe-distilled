package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

// defaultComposeTimeout bounds Compose-adjacent remote filesystem operations.
const defaultComposeTimeout = 10 * time.Minute

// DeployCompose writes runtime artifacts and runs docker compose up for a project.
func (c Checker) DeployCompose(ctx context.Context, marquee domain.Marquee, project string, _ int64, composeYAML string) error {
	fsys := c.remoteFS()
	ctx, cancel := withRuntimeTimeout(ctx, defaultComposeTimeout)
	defer cancel()

	base, err := playgroundBase(project)
	if err != nil {
		return err
	}
	if err := c.ensureTraefik(ctx, fsys, c.dockerRuntime(), marquee); err != nil {
		return err
	}
	if err := c.preparePlaygroundWorkspace(ctx, fsys, marquee, base, project); err != nil {
		return err
	}
	if err := fsys.WriteRemoteFile(ctx, marquee, base+"/compose.yml", []byte(composeYAML), 0o644); err != nil {
		return err
	}
	if err := c.composeRuntime().Up(ctx, marquee, project, base, composeYAML); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	return nil
}

// PreparePlaygroundWorkspace writes files needed before source sync or builds.
func (c Checker) PreparePlaygroundWorkspace(ctx context.Context, marquee domain.Marquee, project string, _ int64) error {
	ctx, cancel := withRuntimeTimeout(ctx, defaultComposeTimeout)
	defer cancel()

	base, err := playgroundBase(project)
	if err != nil {
		return err
	}
	return c.preparePlaygroundWorkspace(ctx, c.remoteFS(), marquee, base, project)
}

// preparePlaygroundWorkspace writes /opt/fibe files shared by source sync, builds, and Compose.
func (c Checker) preparePlaygroundWorkspace(ctx context.Context, fsys RemoteFS, marquee domain.Marquee, base string, project string) error {
	if err := fsys.MkdirAll(ctx, marquee, base+"/props", 0o755); err != nil {
		return err
	}
	dockerConfigDir := optfibe.DockerConfigDir(project)
	if err := fsys.MkdirAll(ctx, marquee, dockerConfigDir, 0o700); err != nil {
		return err
	}
	if err := fsys.MkdirAll(ctx, marquee, optfibe.BuildsPath, 0o755); err != nil {
		return err
	}
	if err := c.writeDockerConfigDir(ctx, fsys, marquee, dockerConfigDir); err != nil {
		return err
	}
	return nil
}

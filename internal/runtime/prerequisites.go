package runtime

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

// prerequisiteTimeout bounds the fast host capability checks before deploy.
const prerequisiteTimeout = 20 * time.Second

// localBootstrapTimeout bounds startup checks for the only supported runtime host.
const localBootstrapTimeout = 2 * time.Minute

// hostPathProbeImage is a tiny image used to verify that /opt/fibe is mounted
// at the same path from the Docker daemon's point of view.
const hostPathProbeImage = "alpine:3.24"

// dockerSocketPath is the required local Docker socket path.
var dockerSocketPath = "/var/run/docker.sock"

// EnsureLocalRuntimeBootstrap verifies and starts the local runtime host.
func (c Checker) EnsureLocalRuntimeBootstrap(ctx context.Context, marquee domain.Marquee) error {
	ctx, cancel := context.WithTimeout(ctx, localBootstrapTimeout)
	defer cancel()

	if err := c.ensureDockerSocket(); err != nil {
		return err
	}
	if err := c.EnsurePrerequisites(ctx, marquee); err != nil {
		return err
	}
	if err := c.ensureDockerCLI(ctx, marquee); err != nil {
		return err
	}
	if err := c.ensureHostPathParity(ctx, marquee); err != nil {
		return err
	}
	if err := c.ensureTraefik(ctx, c.remoteFS(), c.dockerRuntime(), marquee); err != nil {
		return err
	}
	return nil
}

// EnsurePrerequisites verifies that the Marquee can run fibe-distilled workloads.
func (c Checker) EnsurePrerequisites(ctx context.Context, marquee domain.Marquee) error {
	ctx, cancel := context.WithTimeout(ctx, prerequisiteTimeout)
	defer cancel()

	if err := c.dockerRuntime().Ping(ctx, marquee); err != nil {
		return fmt.Errorf("docker daemon ping failed: %w", err)
	}
	fsys := c.remoteFS()
	for _, dir := range prerequisiteDirs() {
		if err := fsys.MkdirAll(ctx, marquee, dir, 0o755); err != nil {
			return fmt.Errorf("prepare runtime directory %s: %w", dir, err)
		}
	}
	return nil
}

// ensureDockerSocket verifies the required local Docker socket path exists.
func (c Checker) ensureDockerSocket() error {
	info, err := os.Stat(dockerSocketPath)
	if err != nil {
		return fmt.Errorf("docker socket %s is required: %w", dockerSocketPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("docker socket %s is a directory", dockerSocketPath)
	}
	return nil
}

// ensureDockerCLI verifies the Docker CLI and Compose plugin are available.
func (c Checker) ensureDockerCLI(ctx context.Context, marquee domain.Marquee) error {
	checks := []struct {
		name    string
		command string
	}{
		{name: "docker CLI", command: "docker version --format '{{.Client.Version}}'"},
		{name: "docker compose plugin", command: "docker compose version --short"},
	}
	for _, check := range checks {
		result, err := c.executor().Run(ctx, marquee, check.command)
		if err != nil {
			return fmt.Errorf("%s check failed: %w", check.name, commandOutputError(result, err))
		}
	}
	return nil
}

// ensureHostPathParity verifies /opt/fibe is the same path seen by the Docker daemon.
func (c Checker) ensureHostPathParity(ctx context.Context, marquee domain.Marquee) error {
	markerPath := "/opt/fibe/.fibe-distilled-host-path-probe"
	fsys := c.remoteFS()
	if err := fsys.WriteRemoteFile(ctx, marquee, markerPath, []byte("ok\n"), 0o600); err != nil {
		return fmt.Errorf("write /opt/fibe host-path probe: %w", err)
	}
	defer func() {
		_ = fsys.RemoveAll(context.WithoutCancel(ctx), marquee, markerPath)
	}()
	command := "docker run --rm --pull missing -v /opt/fibe:/opt/fibe:ro " +
		ShellQuote(hostPathProbeImage) + " test -f " + ShellQuote(markerPath)
	result, err := c.executor().Run(ctx, marquee, command)
	if err != nil {
		return fmt.Errorf("/opt/fibe must be bind-mounted at /opt/fibe for the Docker daemon: %w", commandOutputError(result, err))
	}
	return nil
}

// prerequisiteDirs returns runtime directories fibe-distilled must own directly.
func prerequisiteDirs() []string {
	return optfibe.RuntimePrerequisiteDirs()
}

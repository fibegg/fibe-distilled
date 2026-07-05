package runtime

import (
	"context"
	"io/fs"
	"os"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// CommandResult captures stdout, stderr, and exit status from a runtime command.
type CommandResult struct {
	// Stdout is the command's standard output.
	Stdout string
	// Stderr is the command's standard error.
	Stderr string
	// ExitCode is the process exit status when known.
	ExitCode int
}

// Executor is the command and file transport used by runtime operations.
type Executor interface {
	// Run executes one shell command on the configured runtime host.
	Run(ctx context.Context, marquee domain.Marquee, command string) (CommandResult, error)
	// WriteFile writes content to one runtime path on the configured host.
	WriteFile(ctx context.Context, marquee domain.Marquee, remotePath string, content string) (CommandResult, error)
}

// RemoteFS is the typed filesystem boundary for Marquee runtime files.
type RemoteFS interface {
	// MkdirAll creates a runtime directory tree.
	MkdirAll(ctx context.Context, marquee domain.Marquee, remotePath string, perm os.FileMode) error
	// WriteRemoteFile writes bytes to a runtime path.
	WriteRemoteFile(ctx context.Context, marquee domain.Marquee, remotePath string, content []byte, perm os.FileMode) error
	// ReadRemoteFile reads bytes from a runtime path.
	ReadRemoteFile(ctx context.Context, marquee domain.Marquee, remotePath string) ([]byte, error)
	// RemoveAll removes a runtime path tree.
	RemoveAll(ctx context.Context, marquee domain.Marquee, remotePath string) error
	// Rename moves a runtime path.
	Rename(ctx context.Context, marquee domain.Marquee, oldPath string, newPath string) error
	// Chmod changes runtime path permissions.
	Chmod(ctx context.Context, marquee domain.Marquee, remotePath string, perm os.FileMode) error
	// Stat returns metadata for a runtime path.
	Stat(ctx context.Context, marquee domain.Marquee, remotePath string) (fs.FileInfo, error)
	// ReadDir lists metadata for entries in a runtime directory.
	ReadDir(ctx context.Context, marquee domain.Marquee, remotePath string) ([]fs.FileInfo, error)
}

// DockerRuntime is the typed Docker Engine boundary for the local runtime daemon.
type DockerRuntime interface {
	// Ping verifies Docker daemon reachability.
	Ping(ctx context.Context, marquee domain.Marquee) error
	// ImageMetadata returns fibe-distilled build labels for an image.
	ImageMetadata(ctx context.Context, marquee domain.Marquee, imageRef string) (ImageMetadata, bool, error)
	// EnsureTraefik ensures the managed Traefik runtime exists.
	EnsureTraefik(ctx context.Context, marquee domain.Marquee, args []string) error
	// CleanupProject removes Docker leftovers for a Compose project.
	CleanupProject(ctx context.Context, marquee domain.Marquee, project string, removeVolumes bool) error
}

// ComposeRuntime is the typed Docker Compose boundary for project lifecycle operations.
type ComposeRuntime interface {
	// Up creates and starts a Compose project.
	Up(ctx context.Context, marquee domain.Marquee, project string, base string, composeYAML string) error
	// Start starts a Compose project.
	Start(ctx context.Context, marquee domain.Marquee, project string, base string, composeYAML string) error
	// Stop stops a Compose project.
	Stop(ctx context.Context, marquee domain.Marquee, project string, base string, composeYAML string) error
	// Down removes a Compose project.
	Down(ctx context.Context, marquee domain.Marquee, project string, base string, composeYAML string, removeVolumes bool) error
	// Logs returns Compose logs.
	Logs(ctx context.Context, marquee domain.Marquee, project string, base string, composeYAML string, service string, tail string) ([]string, error)
	// Services returns Compose service state.
	Services(ctx context.Context, marquee domain.Marquee, project string, base string, composeYAML string) ([]domain.PlaygroundServiceInfo, error)
}

// GitRuntime is the typed Git boundary for source checkout operations.
type GitRuntime interface {
	// Sync clones or updates one source checkout.
	Sync(ctx context.Context, marquee domain.Marquee, req GitSyncRequest) error
	// DirtyPaths returns source paths with dirty checkout state.
	DirtyPaths(ctx context.Context, marquee domain.Marquee, project string, sourcePaths []string) ([]string, error)
	// Head returns the current checkout commit SHA.
	Head(ctx context.Context, marquee domain.Marquee, project string, sourcePath string) (string, error)
}

// DockerBuilder is the only runtime boundary allowed to execute docker build shell.
type DockerBuilder interface {
	// Build executes one docker build.
	Build(ctx context.Context, marquee domain.Marquee, req DockerBuildRequest) (CommandResult, error)
}

// Checker coordinates local Docker, Compose, Traefik, and filesystem checks.
type Checker struct {
	// Executor overrides the default local executor for tests.
	Executor Executor
	// FS overrides the default local filesystem.
	FS RemoteFS
	// Docker overrides the default Docker Engine SDK transport.
	Docker DockerRuntime
	// Compose overrides the default Docker Compose CLI runtime.
	Compose ComposeRuntime
	// Git overrides the default go-git source runtime.
	Git GitRuntime
	// Builder overrides the single docker build shell-out.
	Builder DockerBuilder
	// DockerHubUsername authenticates Docker pulls/builds when set.
	DockerHubUsername string
	// DockerHubToken authenticates Docker pulls/builds when set.
	DockerHubToken string
	// InstanceID is retained for API/server identity compatibility; runtime
	// ownership no longer depends on per-server remote markers.
	InstanceID string
}

// executor returns the configured executor or a local executor.
func (c Checker) executor() Executor {
	if c.Executor != nil {
		return c.Executor
	}
	return LocalExecutor{}
}

// ExecutorOrDefault returns the configured executor or the default local executor.
func (c Checker) ExecutorOrDefault() Executor {
	return c.executor()
}

// remoteFS returns the configured typed filesystem or the local default.
func (c Checker) remoteFS() RemoteFS {
	if c.FS != nil {
		return c.FS
	}
	if fs, ok := c.Executor.(RemoteFS); ok {
		return fs
	}
	return LocalFS{}
}

// dockerRuntime returns the configured Docker runtime or the local Docker SDK default.
func (c Checker) dockerRuntime() DockerRuntime {
	if c.Docker != nil {
		return c.Docker
	}
	if docker, ok := c.Executor.(DockerRuntime); ok {
		return docker
	}
	return DockerTransport{DockerHubUsername: c.DockerHubUsername, DockerHubToken: c.DockerHubToken}
}

// composeRuntime returns the configured Compose runtime or the local Compose CLI default.
func (c Checker) composeRuntime() ComposeRuntime {
	if c.Compose != nil {
		return c.Compose
	}
	if compose, ok := c.Executor.(ComposeRuntime); ok {
		return compose
	}
	return LocalComposeRuntime{Executor: c.executor()}
}

// gitRuntime returns the configured Git runtime or the go-git local checkout default.
func (c Checker) gitRuntime() GitRuntime {
	if c.Git != nil {
		return c.Git
	}
	if git, ok := c.Executor.(GitRuntime); ok {
		return git
	}
	return GoGitRuntime{FS: c.remoteFS()}
}

// dockerBuilder returns the configured builder or the centralized local build shell.
func (c Checker) dockerBuilder() DockerBuilder {
	if c.Builder != nil {
		return c.Builder
	}
	if builder, ok := c.Executor.(DockerBuilder); ok {
		return builder
	}
	return LocalDockerBuilder{Executor: c.executor()}
}

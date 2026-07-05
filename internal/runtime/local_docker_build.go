package runtime

import (
	"context"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// DockerBuildRequest carries one Marquee-local Docker build invocation.
type DockerBuildRequest struct {
	// Project is the Compose project owning the build.
	Project string
	// Base is the local Playground workspace path.
	Base string
	// Args are the docker build CLI arguments.
	Args []string
}

// LocalDockerBuilder runs the intentional local Docker CLI build.
type LocalDockerBuilder struct {
	// Executor overrides the local command executor.
	Executor Executor
}

// Build runs docker build on the local host and returns separated output.
func (b LocalDockerBuilder) Build(ctx context.Context, marquee domain.Marquee, req DockerBuildRequest) (CommandResult, error) {
	command := localDockerBuildShell(req.Project, req.Base, req.Args)
	return b.exec().Run(ctx, marquee, command)
}

// exec returns the configured executor or the default local executor.
func (b LocalDockerBuilder) exec() Executor {
	if b.Executor != nil {
		return b.Executor
	}
	return LocalExecutor{}
}

// localDockerBuildCommand is the only production runtime shell command that is
// intentionally allowed to execute a Docker CLI program on the local runtime host.
//
// Every other Docker and Compose operation should move to typed Docker Engine
// and Compose API calls. Docker image builds are the exception for now because
// the build context is already a local runtime checkout under
// /opt/fibe/playgrounds/<project>/props/<repo>/<branch>. When a user runs
// `docker build /opt/fibe/...` on that same host, the Docker CLI does important
// client-side work before the daemon builds the image: it walks the context,
// applies .dockerignore, packages the context, and streams it to the daemon.
//
// If fibe-distilled replaced this call with Engine API ImageBuild directly, the
// build would still run on the daemon, but fibe-distilled would become the Docker
// client responsible for packaging the local checkout into a build
// context stream. That is a separate implementation of Docker CLI context
// behavior, including .dockerignore and edge cases around symlinks, file modes,
// and Dockerfile-relative paths. Keeping this one CLI call preserves the
// Docker-local build-context packaging semantics while the rest of the runtime
// moves away from shell programs.
//
// Keep all `docker build` string construction in this function. Tests and
// linters should reject any additional production `docker build` occurrences.
func localDockerBuildCommand(base string, args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, ShellQuote(arg))
	}
	return "DOCKER_CONFIG=" + ShellQuote(base+"/docker-config") + " docker build " + strings.Join(quoted, " ")
}

// localDockerBuildShell wraps the local docker build command.
func localDockerBuildShell(project string, base string, args []string) string {
	return "project=" + ShellQuote(project) +
		" base=" + ShellQuote(base) +
		" sh -eu <<'FIBE_DISTILLED_BUILD_IMAGE'\n" +
		localDockerBuildCommand(base, args) +
		"\nFIBE_DISTILLED_BUILD_IMAGE"
}

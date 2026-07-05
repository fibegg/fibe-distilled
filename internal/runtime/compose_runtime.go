package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// LocalComposeRuntime drives Docker Compose through the local CLI.
type LocalComposeRuntime struct {
	// Executor overrides the local command executor.
	Executor Executor
}

// Up creates and starts a Compose project.
func (r LocalComposeRuntime) Up(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	return r.run(ctx, marquee, composeUpCommand(project, base))
}

// Start starts or recreates a Compose project from its current model.
func (r LocalComposeRuntime) Start(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	return r.run(ctx, marquee, composeUpCommand(project, base))
}

// Stop stops a Compose project.
func (r LocalComposeRuntime) Stop(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	return r.run(ctx, marquee, composeStopCommand(project, base))
}

// Down stops and removes a Compose project.
func (r LocalComposeRuntime) Down(ctx context.Context, marquee domain.Marquee, project string, base string, _ string, removeVolumes bool) error {
	args := "down --remove-orphans"
	if removeVolumes {
		args += " -v"
	}
	return r.run(ctx, marquee, composeBaseCommand(project, base)+" "+args)
}

// Logs returns service logs from Compose.
func (r LocalComposeRuntime) Logs(ctx context.Context, marquee domain.Marquee, project string, base string, _ string, service string, tail string) ([]string, error) {
	args := []string{composeBaseCommand(project, base), "logs", "--no-color", "--tail", ShellQuote(tail)}
	if strings.TrimSpace(service) != "" {
		args = append(args, ShellQuote(service))
	}
	result, err := r.exec().Run(ctx, marquee, strings.Join(args, " "))
	if err != nil {
		return nil, commandOutputError(result, err)
	}
	return splitLogLines(result.Stdout), nil
}

// Services returns Compose service state.
func (r LocalComposeRuntime) Services(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) ([]domain.PlaygroundServiceInfo, error) {
	result, err := r.exec().Run(ctx, marquee, composeBaseCommand(project, base)+" ps --all --format json")
	if err != nil {
		return nil, commandOutputError(result, err)
	}
	states, err := ParseComposeServiceStates(result.Stdout)
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		states, err = r.dockerProjectServiceStates(ctx, marquee, project)
		if err != nil {
			return nil, err
		}
	}
	return composeStatesToDomain(states), nil
}

// run executes a Compose shell command and returns stderr/stdout as the error when available.
func (r LocalComposeRuntime) run(ctx context.Context, marquee domain.Marquee, command string) error {
	result, err := r.exec().Run(ctx, marquee, command)
	if err != nil {
		return commandOutputError(result, err)
	}
	return nil
}

// exec returns the configured executor or the default local executor.
func (r LocalComposeRuntime) exec() Executor {
	if r.Executor != nil {
		return r.Executor
	}
	return LocalExecutor{}
}

// dockerProjectServiceStates falls back to Docker labels when Compose JSON is empty.
func (r LocalComposeRuntime) dockerProjectServiceStates(ctx context.Context, marquee domain.Marquee, project string) ([]ComposeServiceState, error) {
	result, err := r.exec().Run(ctx, marquee, dockerProjectServicesCommand(project))
	if err != nil {
		return nil, commandOutputError(result, err)
	}
	return ParseDockerPSServiceRows(result.Stdout)
}

// composeUpCommand builds the local detached compose up command for one project.
func composeUpCommand(project string, base string) string {
	return "cd " + ShellQuote(base) + " && DOCKER_CONFIG=" + ShellQuote(base+"/docker-config") + " docker compose -f compose.yml -p " + ShellQuote(project) + " up -d --remove-orphans --pull missing"
}

// composeStopCommand builds the local compose stop command for one project.
func composeStopCommand(project string, base string) string {
	return composeBaseCommand(project, base) + " stop"
}

// composeBaseCommand builds the shared local compose command prefix.
func composeBaseCommand(project string, base string) string {
	return "cd " + ShellQuote(base) + " && docker compose -f compose.yml -p " + ShellQuote(project)
}

// dockerProjectServicesCommand emits stable tab-separated Docker service fields.
func dockerProjectServicesCommand(project string) string {
	template := `{{.Label "com.docker.compose.service"}}	{{.Names}}	{{.Image}}	{{.State}}	{{.HealthStatus}}	{{.Status}}`
	return "docker ps -a --filter " + ShellQuote("label=com.docker.compose.project="+project) + " --format " + ShellQuote(template)
}

// commandOutputError prefers remote command output over generic transport errors.
func commandOutputError(result CommandResult, err error) error {
	output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if output != "" {
		return errors.New(output)
	}
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("command exited with status %d", result.ExitCode)
	}
	return nil
}

// composeStatesToDomain converts docker compose ps JSON state into API service state.
func composeStatesToDomain(states []ComposeServiceState) []domain.PlaygroundServiceInfo {
	services := make([]domain.PlaygroundServiceInfo, 0, len(states))
	for _, state := range states {
		name := strings.TrimSpace(firstNonEmpty(state.Service, state.Name))
		if name == "" {
			continue
		}
		exitCode := state.ExitCode
		var exit *int
		if exitCode != 0 {
			exit = &exitCode
		}
		status := strings.TrimSpace(state.State)
		services = append(services, domain.PlaygroundServiceInfo{
			Name:     name,
			Status:   status,
			Image:    state.Image,
			Health:   normalizedServiceHealth(state.Health),
			Running:  strings.EqualFold(status, "running"),
			ExitCode: exit,
		})
	}
	return services
}

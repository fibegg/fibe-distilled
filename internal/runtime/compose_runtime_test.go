package runtime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
)

func TestLocalComposeRuntimeServicesFallsBackToDockerLabelsWhenComposeJSONEmpty(t *testing.T) {
	exec := &composeRuntimeExecutor{
		responses: []composeRuntimeResponse{
			{contains: "docker compose -f compose.yml -p 'demo--1' ps --all --format json", result: runtime.CommandResult{}},
			{contains: "docker ps -a --filter 'label=com.docker.compose.project=demo--1'", result: runtime.CommandResult{
				Stdout: "web\tdemo--1-web-1\tbusybox:1.36\texited\tnone\tExited (255) 2 minutes ago\nworker\tdemo--1-worker-1\talpine:3.24\trunning\thealthy\tUp 2 minutes\n",
			}},
		},
	}
	services, err := (runtime.LocalComposeRuntime{Executor: exec}).Services(context.Background(), domain.Marquee{}, "demo--1", "/opt/fibe/playgrounds/demo--1", "")
	if err != nil {
		t.Fatalf("services: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected two services, got %#v", services)
	}
	assertRuntimeService(t, services[0], "web", "exited", "", false, 255)
	assertRuntimeService(t, services[1], "worker", "running", "healthy", true, 0)

	seen := strings.Join(exec.commands, "\n")
	for _, want := range []string{
		"docker compose -f compose.yml -p 'demo--1' ps --all --format json",
		"docker ps -a --filter 'label=com.docker.compose.project=demo--1'",
		`{{.Label "com.docker.compose.service"}}`,
	} {
		if !strings.Contains(seen, want) {
			t.Fatalf("expected command containing %q:\n%s", want, seen)
		}
	}
}

func TestParseDockerPSServiceRowsRejectsMalformedRows(t *testing.T) {
	_, err := runtime.ParseDockerPSServiceRows("web\tcontainer\timage")
	if err == nil || !strings.Contains(err.Error(), "expected 6 tab-separated fields") {
		t.Fatalf("expected malformed row error, got %v", err)
	}
}

type composeRuntimeExecutor struct {
	commands  []string
	responses []composeRuntimeResponse
}

type composeRuntimeResponse struct {
	contains string
	result   runtime.CommandResult
	err      error
}

func (e *composeRuntimeExecutor) Run(_ context.Context, _ domain.Marquee, command string) (runtime.CommandResult, error) {
	e.commands = append(e.commands, command)
	for _, response := range e.responses {
		if strings.Contains(command, response.contains) {
			return response.result, response.err
		}
	}
	return runtime.CommandResult{}, nil
}

func (e *composeRuntimeExecutor) WriteFile(context.Context, domain.Marquee, string, string) (runtime.CommandResult, error) {
	return runtime.CommandResult{}, nil
}

func assertRuntimeService(t *testing.T, service domain.PlaygroundServiceInfo, name string, status string, health string, running bool, exitCode int) {
	t.Helper()
	if service.Name != name || service.Status != status || service.Health != health || service.Running != running {
		t.Fatalf("unexpected service state: %#v", service)
	}
	if exitCode == 0 {
		if service.ExitCode != nil {
			t.Fatalf("expected nil exit code for %s, got %#v", name, service.ExitCode)
		}
		return
	}
	if service.ExitCode == nil || *service.ExitCode != exitCode {
		t.Fatalf("expected exit code %d for %s, got %#v", exitCode, name, service.ExitCode)
	}
}

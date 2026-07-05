package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := ShellQuote("one'two")
	if got != `'one'"'"'two'` {
		t.Fatalf("ShellQuote() = %q", got)
	}
}

func TestLocalDockerBuildShellWrapsCommand(t *testing.T) {
	got := localDockerBuildShell(
		"demo--1",
		"/opt/fibe/playgrounds/demo--1",
		[]string{"-t", "fibe-distilled/demo/web:abcdef", "/opt/fibe/playgrounds/demo--1/props/acme-demo/main"},
	)
	for _, want := range []string{
		"project='demo--1'",
		"base='/opt/fibe/playgrounds/demo--1'",
		"DOCKER_CONFIG='/opt/fibe/playgrounds/demo--1/docker-config'",
		"docker build '-t' 'fibe-distilled/demo/web:abcdef'",
		"/opt/fibe/playgrounds/demo--1/props/acme-demo/main",
		"FIBE_DISTILLED_BUILD_IMAGE",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("localDockerBuildShell() missing %q:\n%s", want, got)
		}
	}
}

func TestLocalExecutorRunKeepsStdoutAndStderrSeparate(t *testing.T) {
	result, err := (LocalExecutor{}).Run(context.Background(), domain.Marquee{}, `printf 'out'; printf 'err' >&2; exit 7`)
	if err == nil {
		t.Fatal("Run should return command error")
	}
	if result.Stdout != "out" || result.Stderr != "err" || result.ExitCode != 7 {
		t.Fatalf("unexpected command result: %#v", result)
	}
}

func TestLocalExecutorWriteFile(t *testing.T) {
	path := t.TempDir() + "/nested/file.txt"
	result, err := (LocalExecutor{}).WriteFile(context.Background(), domain.Marquee{}, path, "content")
	if err != nil {
		t.Fatalf("WriteFile: result=%#v err=%v", result, err)
	}
	content, err := (LocalFS{}).ReadRemoteFile(context.Background(), domain.Marquee{}, path)
	if err != nil {
		t.Fatalf("ReadRemoteFile: %v", err)
	}
	if string(content) != "content" {
		t.Fatalf("content = %q", content)
	}
}

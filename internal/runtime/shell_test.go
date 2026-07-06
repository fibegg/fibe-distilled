package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

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

func TestLocalExecutorRunCancelsBackgroundChildren(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("process-group cancellation is Unix-specific")
	}
	marker := filepath.Join(t.TempDir(), "child-survived")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	result, err := (LocalExecutor{}).Run(ctx, domain.Marquee{}, "sleep 1 && touch "+ShellQuote(marker)+" & wait")
	if err == nil {
		t.Fatalf("Run should return context cancellation error, result=%#v", result)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Run took %s after context cancellation; result=%#v err=%v", elapsed, result, err)
	}

	time.Sleep(1200 * time.Millisecond)
	_, statErr := os.Stat(marker)
	if statErr == nil {
		t.Fatalf("background child survived context cancellation and wrote %s", marker)
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat marker: %v", statErr)
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

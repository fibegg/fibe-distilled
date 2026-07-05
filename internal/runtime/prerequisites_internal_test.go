package runtime

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

func TestEnsureDockerSocketFailsWhenMissing(t *testing.T) {
	original := dockerSocketPath
	t.Cleanup(func() { dockerSocketPath = original })
	dockerSocketPath = filepath.Join(t.TempDir(), "missing.sock")

	err := Checker{}.ensureDockerSocket()
	if err == nil || !strings.Contains(err.Error(), "docker socket "+dockerSocketPath+" is required") {
		t.Fatalf("expected missing socket failure, got %v", err)
	}
}

func TestEnsureDockerSocketFailsWhenDirectory(t *testing.T) {
	original := dockerSocketPath
	t.Cleanup(func() { dockerSocketPath = original })
	dockerSocketPath = t.TempDir()

	err := Checker{}.ensureDockerSocket()
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory socket failure, got %v", err)
	}
}

func TestEnsureDockerSocketAcceptsSocketPathFile(t *testing.T) {
	original := dockerSocketPath
	t.Cleanup(func() { dockerSocketPath = original })
	dockerSocketPath = filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(dockerSocketPath, []byte("socket placeholder"), 0o600); err != nil {
		t.Fatalf("write socket placeholder: %v", err)
	}

	if err := (Checker{}).ensureDockerSocket(); err != nil {
		t.Fatalf("expected placeholder file to satisfy path check, got %v", err)
	}
}

func TestEnsureDockerCLIChecksClientAndComposePlugin(t *testing.T) {
	exec := &prerequisiteExecutor{}
	checker := Checker{Executor: exec}

	if err := checker.ensureDockerCLI(context.Background(), domain.Marquee{Name: "default"}); err != nil {
		t.Fatalf("ensure docker cli: %v", err)
	}

	got := strings.Join(exec.seen, "\n")
	for _, want := range []string{
		"docker version --format '{{.Client.Version}}'",
		"docker compose version --short",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in commands:\n%s", want, got)
		}
	}
}

func TestEnsureDockerCLIFailsWhenComposePluginMissing(t *testing.T) {
	exec := &prerequisiteExecutor{
		failExact: map[string]prerequisiteFailure{
			"docker compose version --short": {
				result: CommandResult{Stderr: "docker: 'compose' is not a docker command"},
				err:    errors.New("exit status 1"),
			},
		},
	}
	checker := Checker{Executor: exec}

	err := checker.ensureDockerCLI(context.Background(), domain.Marquee{Name: "default"})
	if err == nil || !strings.Contains(err.Error(), "docker compose plugin check failed") || !strings.Contains(err.Error(), "compose") {
		t.Fatalf("expected compose plugin failure, got %v", err)
	}
}

func TestEnsureHostPathParityWritesProbeRunsDockerAndRemovesProbe(t *testing.T) {
	exec := &prerequisiteExecutor{}
	fsys := &prerequisiteFS{}
	checker := Checker{Executor: exec, FS: fsys}

	if err := checker.ensureHostPathParity(context.Background(), domain.Marquee{Name: "default"}); err != nil {
		t.Fatalf("ensure host path parity: %v", err)
	}

	if got := fsys.writes["/opt/fibe/.fibe-distilled-host-path-probe"]; got != "ok\n" {
		t.Fatalf("unexpected probe content %q", got)
	}
	if got := fsys.writeModes["/opt/fibe/.fibe-distilled-host-path-probe"]; got != 0o600 {
		t.Fatalf("unexpected probe mode %o", got)
	}
	if len(fsys.removed) != 1 || fsys.removed[0] != "/opt/fibe/.fibe-distilled-host-path-probe" {
		t.Fatalf("expected probe cleanup, got %#v", fsys.removed)
	}
	command := strings.Join(exec.seen, "\n")
	for _, want := range []string{
		"docker run --rm --pull missing",
		"-v /opt/fibe:/opt/fibe:ro",
		hostPathProbeImage,
		"test -f '/opt/fibe/.fibe-distilled-host-path-probe'",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("expected %q in host-path probe command:\n%s", want, command)
		}
	}
}

func TestEnsureHostPathParityFailsWhenOptFibeIsUnwritable(t *testing.T) {
	checker := Checker{
		Executor: &prerequisiteExecutor{},
		FS:       &prerequisiteFS{writeErr: errors.New("permission denied")},
	}

	err := checker.ensureHostPathParity(context.Background(), domain.Marquee{Name: "default"})
	if err == nil || !strings.Contains(err.Error(), "write /opt/fibe host-path probe") {
		t.Fatalf("expected write failure, got %v", err)
	}
}

func TestEnsureHostPathParityFailsWhenDaemonCannotSeeOptFibe(t *testing.T) {
	exec := &prerequisiteExecutor{
		failContains: map[string]prerequisiteFailure{
			"docker run --rm --pull missing": {
				result: CommandResult{Stderr: "No such file or directory"},
				err:    errors.New("exit status 1"),
			},
		},
	}
	fsys := &prerequisiteFS{}
	checker := Checker{Executor: exec, FS: fsys}

	err := checker.ensureHostPathParity(context.Background(), domain.Marquee{Name: "default"})
	if err == nil || !strings.Contains(err.Error(), "/opt/fibe must be bind-mounted at /opt/fibe") {
		t.Fatalf("expected host-path parity failure, got %v", err)
	}
	if len(fsys.removed) != 1 || fsys.removed[0] != "/opt/fibe/.fibe-distilled-host-path-probe" {
		t.Fatalf("expected failed probe cleanup, got %#v", fsys.removed)
	}
}

type prerequisiteFailure struct {
	result CommandResult
	err    error
}

type prerequisiteExecutor struct {
	seen         []string
	failExact    map[string]prerequisiteFailure
	failContains map[string]prerequisiteFailure
}

func (e *prerequisiteExecutor) Run(_ context.Context, _ domain.Marquee, command string) (CommandResult, error) {
	e.seen = append(e.seen, command)
	if failure, ok := e.failExact[command]; ok {
		return failure.result, failure.err
	}
	for needle, failure := range e.failContains {
		if strings.Contains(command, needle) {
			return failure.result, failure.err
		}
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

func (e *prerequisiteExecutor) WriteFile(_ context.Context, _ domain.Marquee, remotePath string, content string) (CommandResult, error) {
	e.seen = append(e.seen, "write:"+remotePath+":"+content)
	return CommandResult{Stdout: "ok\n"}, nil
}

type prerequisiteFS struct {
	writes     map[string]string
	writeModes map[string]os.FileMode
	writeErr   error
	removed    []string
}

func (f *prerequisiteFS) MkdirAll(context.Context, domain.Marquee, string, os.FileMode) error {
	return nil
}

func (f *prerequisiteFS) WriteRemoteFile(_ context.Context, _ domain.Marquee, remotePath string, content []byte, perm os.FileMode) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.writes == nil {
		f.writes = map[string]string{}
	}
	if f.writeModes == nil {
		f.writeModes = map[string]os.FileMode{}
	}
	f.writes[remotePath] = string(content)
	f.writeModes[remotePath] = perm
	return nil
}

func (f *prerequisiteFS) ReadRemoteFile(context.Context, domain.Marquee, string) ([]byte, error) {
	return nil, ErrRemoteFileMissing
}

func (f *prerequisiteFS) RemoveAll(_ context.Context, _ domain.Marquee, remotePath string) error {
	f.removed = append(f.removed, remotePath)
	return nil
}

func (f *prerequisiteFS) Rename(context.Context, domain.Marquee, string, string) error {
	return nil
}

func (f *prerequisiteFS) Chmod(context.Context, domain.Marquee, string, os.FileMode) error {
	return nil
}

func (f *prerequisiteFS) Stat(context.Context, domain.Marquee, string) (fs.FileInfo, error) {
	return nil, ErrRemoteFileMissing
}

func (f *prerequisiteFS) ReadDir(context.Context, domain.Marquee, string) ([]fs.FileInfo, error) {
	return nil, ErrRemoteFileMissing
}

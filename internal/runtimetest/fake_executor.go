package runtimetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
)

// FakeExecutor records runtime operations and returns configured results.
type FakeExecutor struct {
	mu sync.Mutex
	// Results maps exact commands to command results.
	Results map[string]runtime.CommandResult
	// Errors maps exact commands to execution errors.
	Errors map[string]error
	// ResultContains maps command substrings to command results.
	ResultContains map[string]runtime.CommandResult
	// ErrorContains maps command substrings to execution errors.
	ErrorContains map[string]error
	// ReadFiles maps runtime paths to fake file content.
	ReadFiles map[string]string
	// ReadErrors maps runtime paths to fake read errors.
	ReadErrors map[string]error
	// Seen records command, write, and read operations in order.
	Seen []string
}

// Run records a command and returns an exact, contains, list, or default result.
func (f *FakeExecutor) Run(_ context.Context, _ domain.Marquee, command string) (runtime.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, command)
	if result, ok, err := f.exactResult(command); ok {
		return result, err
	}
	if result, ok, err := f.containsResult(command); ok {
		return result, err
	}
	return runtime.CommandResult{Stdout: "ok"}, nil
}

// WriteFile records a runtime file write.
func (f *FakeExecutor) WriteFile(_ context.Context, _ domain.Marquee, remotePath string, content string) (runtime.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, "write:"+remotePath+":"+content)
	f.writeRemote(remotePath, content)
	return runtime.CommandResult{Stdout: "ok"}, nil
}

// ReadFile records a runtime file read and returns configured content or absence.
func (f *FakeExecutor) ReadFile(_ context.Context, _ domain.Marquee, remotePath string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, "read:"+remotePath)
	if f.ReadErrors != nil && f.ReadErrors[remotePath] != nil {
		return "", f.ReadErrors[remotePath]
	}
	if f.ReadFiles != nil {
		if content, ok := f.ReadFiles[remotePath]; ok {
			return content, nil
		}
	}
	if strings.HasSuffix(remotePath, "/compose.yml") {
		return "services:\n  web:\n    image: nginx\n", nil
	}
	return "", runtime.ErrRemoteFileMissing
}

// MkdirAll records a typed runtime directory creation.
func (f *FakeExecutor) MkdirAll(_ context.Context, _ domain.Marquee, remotePath string, _ os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, "mkdir -p "+runtime.ShellQuote(remotePath))
	return nil
}

// WriteRemoteFile records a typed runtime file write.
func (f *FakeExecutor) WriteRemoteFile(_ context.Context, _ domain.Marquee, remotePath string, content []byte, _ os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, "write:"+remotePath+":"+string(content))
	f.writeRemote(remotePath, string(content))
	return nil
}

// ReadRemoteFile records a typed runtime file read.
func (f *FakeExecutor) ReadRemoteFile(_ context.Context, _ domain.Marquee, remotePath string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, "read:"+remotePath)
	if f.ReadErrors != nil && f.ReadErrors[remotePath] != nil {
		return nil, f.ReadErrors[remotePath]
	}
	if f.ReadFiles != nil {
		if content, ok := f.ReadFiles[remotePath]; ok {
			return []byte(content), nil
		}
	}
	if strings.HasSuffix(remotePath, "/compose.yml") {
		return []byte("services:\n  web:\n    image: nginx\n"), nil
	}
	return nil, runtime.ErrRemoteFileMissing
}

// RemoveAll records a typed runtime tree removal.
func (f *FakeExecutor) RemoveAll(_ context.Context, _ domain.Marquee, remotePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, "remove:"+remotePath)
	for path := range f.ReadFiles {
		if path == remotePath || strings.HasPrefix(path, strings.TrimRight(remotePath, "/")+"/") {
			delete(f.ReadFiles, path)
		}
	}
	return nil
}

// Rename records a typed runtime rename.
func (f *FakeExecutor) Rename(_ context.Context, _ domain.Marquee, oldPath string, newPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, "rename:"+oldPath+":"+newPath)
	return nil
}

// Chmod records a typed runtime chmod.
func (f *FakeExecutor) Chmod(_ context.Context, _ domain.Marquee, remotePath string, perm os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Seen = append(f.Seen, fmt.Sprintf("chmod %03o %s", perm.Perm(), runtime.ShellQuote(remotePath)))
	return nil
}

// Stat returns configured fake file metadata when present.
func (f *FakeExecutor) Stat(_ context.Context, _ domain.Marquee, remotePath string) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ReadFiles != nil {
		if _, ok := f.ReadFiles[remotePath]; ok {
			return fakeFileInfo{name: remotePath, mode: 0o644}, nil
		}
	}
	return nil, runtime.ErrRemoteFileMissing
}

// ReadDir lists direct fake child directories under a runtime path.
func (f *FakeExecutor) ReadDir(_ context.Context, _ domain.Marquee, remotePath string) ([]fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	root := strings.TrimRight(remotePath, "/")
	children := map[string]bool{}
	for path := range f.ReadFiles {
		rest, ok := strings.CutPrefix(path, root+"/")
		if !ok || rest == "" {
			continue
		}
		name, _, _ := strings.Cut(rest, "/")
		if strings.TrimSpace(name) != "" {
			children[name] = true
		}
	}
	if len(children) == 0 {
		return nil, runtime.ErrRemoteFileMissing
	}
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]fs.FileInfo, 0, len(names))
	for _, name := range names {
		entries = append(entries, fakeFileInfo{name: name, mode: os.ModeDir | 0o755})
	}
	return entries, nil
}

// Ping records a typed Docker daemon ping.
func (f *FakeExecutor) Ping(ctx context.Context, marquee domain.Marquee) error {
	_, err := f.Run(ctx, marquee, "docker:ping")
	return err
}

// ImageMetadata records a typed Docker image metadata lookup.
func (f *FakeExecutor) ImageMetadata(ctx context.Context, marquee domain.Marquee, imageRef string) (runtime.ImageMetadata, bool, error) {
	command := "docker image inspect " + runtime.ShellQuote(imageRef) + " --format '{{json .Config}}'"
	result, err := f.Run(ctx, marquee, command)
	if err != nil {
		output := strings.ToLower(result.Stdout + "\n" + result.Stderr)
		if strings.Contains(output, "no such image") || strings.Contains(output, "no such object") {
			return runtime.ImageMetadata{}, false, nil
		}
		if text := strings.TrimSpace(result.Stdout + "\n" + result.Stderr); text != "" {
			return runtime.ImageMetadata{}, false, fmt.Errorf("docker image inspect failed: %w: %s", err, text)
		}
		return runtime.ImageMetadata{}, false, fmt.Errorf("docker image inspect failed: %w", err)
	}
	metadata, err := fakeImageMetadata(result.Stdout)
	if err != nil {
		return runtime.ImageMetadata{}, false, err
	}
	return metadata, true, nil
}

// EnsureTraefik records typed Traefik reconciliation.
func (f *FakeExecutor) EnsureTraefik(ctx context.Context, marquee domain.Marquee, args []string) error {
	_, err := f.Run(ctx, marquee, "traefik:ensure:"+strings.Join(args, "\n"))
	return err
}

// CleanupProject records typed Docker project cleanup.
func (f *FakeExecutor) CleanupProject(ctx context.Context, marquee domain.Marquee, project string, removeVolumes bool) error {
	_, err := f.Run(ctx, marquee, fmt.Sprintf("docker:cleanup:%s:volumes=%t", project, removeVolumes))
	return err
}

// ComposeUpCommand builds the fake Compose up/start command text.
func ComposeUpCommand(prefix string, project string, base string, marqueeID int64) string {
	return composePrefix(prefix, project, base, marqueeID) + "cd " + runtime.ShellQuote(base) + " && DOCKER_CONFIG=" + runtime.ShellQuote(base+"/docker-config") + " docker compose -f compose.yml -p " + runtime.ShellQuote(project) + " up -d --remove-orphans --pull missing"
}

// ComposeStopCommand builds the fake Compose stop command text.
func ComposeStopCommand(prefix string, project string, base string, marqueeID int64) string {
	return composePrefix(prefix, project, base, marqueeID) + "cd " + runtime.ShellQuote(base) + " && docker compose -f compose.yml -p " + runtime.ShellQuote(project) + " stop"
}

// ComposeInspectCommand builds the fake Compose service-inspection command text.
func ComposeInspectCommand(project string, base string, marqueeID int64) string {
	return "FIBE_DISTILLED_INSPECT " + project + " " + base + " ps --all --format json base=" + runtime.ShellQuote(base) + " project=" + runtime.ShellQuote(project) + " marquee_id=" + fmt.Sprint(marqueeID)
}

// composePrefix builds optional typed command metadata.
func composePrefix(prefix string, project string, base string, marqueeID int64) string {
	if strings.TrimSpace(prefix) == "" {
		return ""
	}
	return prefix + " project=" + runtime.ShellQuote(project) + " base=" + runtime.ShellQuote(base) + " marquee_id=" + fmt.Sprint(marqueeID) + " "
}

// Up records typed Compose up.
func (f *FakeExecutor) Up(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	_, err := f.Run(ctx, marquee, ComposeUpCommand("FIBE_DISTILLED_UP", project, base, marquee.ID))
	return err
}

// Start records typed Compose start.
func (f *FakeExecutor) Start(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	_, err := f.Run(ctx, marquee, ComposeUpCommand("FIBE_DISTILLED_START", project, base, marquee.ID))
	return err
}

// Stop records typed Compose stop.
func (f *FakeExecutor) Stop(ctx context.Context, marquee domain.Marquee, project string, base string, _ string) error {
	result, err := f.Run(ctx, marquee, ComposeStopCommand("FIBE_DISTILLED_STOP", project, base, marquee.ID))
	if err != nil {
		if output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr); output != "" {
			return errors.New(output)
		}
	}
	return err
}

// Down records typed Compose down.
func (f *FakeExecutor) Down(ctx context.Context, marquee domain.Marquee, project string, base string, _ string, removeVolumes bool) error {
	args := "down --remove-orphans"
	if removeVolumes {
		args += " -v"
	}
	result, err := f.Run(ctx, marquee, "FIBE_DISTILLED_DOWN project="+runtime.ShellQuote(project)+" base="+runtime.ShellQuote(base)+" marquee_id="+fmt.Sprint(marquee.ID)+" cd "+runtime.ShellQuote(base)+" && docker compose -f compose.yml -p "+runtime.ShellQuote(project)+" "+args)
	if err != nil {
		if output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr); output != "" {
			return errors.New(output)
		}
	}
	return err
}

// Logs records typed Compose logs and returns configured lines.
func (f *FakeExecutor) Logs(ctx context.Context, marquee domain.Marquee, project string, base string, _ string, service string, tail string) ([]string, error) {
	result, err := f.Run(ctx, marquee, "FIBE_DISTILLED_LOGS "+project+" "+base+" "+service+" "+tail+" project="+runtime.ShellQuote(project)+" base="+runtime.ShellQuote(base)+" marquee_id="+fmt.Sprint(marquee.ID))
	if err != nil {
		if output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr); output != "" {
			return nil, errors.New(output)
		}
		return nil, err
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return []string{}, nil
	}
	return strings.Split(strings.TrimRight(result.Stdout, "\n"), "\n"), nil
}

// Services records typed Compose ps and returns configured service state.
func (f *FakeExecutor) Services(ctx context.Context, marquee domain.Marquee, project string, base string, composeYAML string) ([]domain.PlaygroundServiceInfo, error) {
	result, err := f.Run(ctx, marquee, ComposeInspectCommand(project, base, marquee.ID))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Stdout) == "" || strings.TrimSpace(result.Stdout) == "ok" {
		return fakeRunningServicesFromCompose(composeYAML), nil
	}
	return fakeComposeServices(result.Stdout)
}

// Sync records typed Git source sync.
func (f *FakeExecutor) Sync(ctx context.Context, marquee domain.Marquee, req runtime.GitSyncRequest) error {
	authMarker := "github_auth=false"
	if strings.TrimSpace(req.GitHubToken) != "" {
		authMarker = "github_auth=true"
	}
	result, err := f.Run(ctx, marquee, "FIBE_DISTILLED_SOURCE_SYNC "+req.TargetPath.String()+" "+req.RepoURL+" "+req.Branch+" "+authMarker)
	if err != nil {
		if output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr); output != "" {
			return errors.New(output)
		}
	}
	return err
}

// DirtyPaths records typed Git dirty checks.
func (f *FakeExecutor) DirtyPaths(ctx context.Context, marquee domain.Marquee, project string, paths []string) ([]string, error) {
	var dirty []string
	for _, sourcePath := range paths {
		if strings.TrimSpace(sourcePath) == "" {
			continue
		}
		if _, err := runtime.NewRemoteCheckoutPath(project, sourcePath); err != nil {
			return nil, errors.New("source dirty path must be an absolute checkout path under this playground")
		}
		result, err := f.Run(ctx, marquee, "FIBE_DISTILLED_SOURCE_DIRTY "+project+" "+sourcePath)
		if err != nil || strings.TrimSpace(result.Stdout+"\n"+result.Stderr) != "" {
			dirty = append(dirty, sourcePath)
		}
	}
	return dirty, nil
}

// Head records typed Git HEAD resolution.
func (f *FakeExecutor) Head(ctx context.Context, marquee domain.Marquee, project string, sourcePath string) (string, error) {
	result, err := f.Run(ctx, marquee, "FIBE_DISTILLED_RESOLVE_COMMIT "+project+" "+sourcePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

// Build records the centralized local Docker build.
func (f *FakeExecutor) Build(ctx context.Context, marquee domain.Marquee, req runtime.DockerBuildRequest) (runtime.CommandResult, error) {
	quoted := make([]string, 0, len(req.Args))
	for _, arg := range req.Args {
		quoted = append(quoted, runtime.ShellQuote(arg))
	}
	return f.Run(ctx, marquee, "FIBE_DISTILLED_BUILD_IMAGE DOCKER_CONFIG="+runtime.ShellQuote(req.Base+"/docker-config")+" docker build "+strings.Join(quoted, " "))
}

// fakeImageMetadata parses image metadata from fake inspect output.
func fakeImageMetadata(raw string) (runtime.ImageMetadata, error) {
	var config map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &config); err != nil {
		return runtime.ImageMetadata{}, fmt.Errorf("parse image config JSON: %w", err)
	}
	if config == nil {
		return runtime.ImageMetadata{}, errors.New("parse image config JSON: expected object")
	}
	labels, _ := config["Labels"].(map[string]any)
	value := func(key string) string {
		text, _ := labels[key].(string)
		return strings.TrimSpace(text)
	}
	return runtime.ImageMetadata{
		CommitSHA:           firstNonEmpty(value("fibe.build.git_commit_sha"), value("fibe.source_commit")),
		BuildIdentityDigest: value("fibe.build.identity_digest"),
	}, nil
}

// fakeComposeServices parses fake Compose service state.
func fakeComposeServices(raw string) ([]domain.PlaygroundServiceInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "ok" {
		return nil, nil
	}
	states, err := runtime.ParseComposeServiceStates(raw)
	if err != nil {
		return nil, err
	}
	services := make([]domain.PlaygroundServiceInfo, 0, len(states))
	for _, state := range states {
		name := firstNonEmpty(state.Service, state.Name)
		if strings.TrimSpace(name) == "" {
			continue
		}
		exitCode := state.ExitCode
		var exit *int
		if strings.EqualFold(state.State, "exited") || strings.EqualFold(state.State, "dead") {
			exit = &exitCode
		}
		services = append(services, domain.PlaygroundServiceInfo{
			Name:     strings.TrimSpace(name),
			Image:    strings.TrimSpace(state.Image),
			Status:   strings.ToLower(strings.TrimSpace(state.State)),
			Health:   strings.ToLower(strings.TrimSpace(state.Health)),
			Running:  strings.EqualFold(strings.TrimSpace(state.State), "running"),
			ExitCode: exit,
		})
	}
	return services, nil
}

// fakeRunningServicesFromCompose derives running service state from Compose YAML.
func fakeRunningServicesFromCompose(composeYAML string) []domain.PlaygroundServiceInfo {
	validation := compose.Validate(composeYAML)
	services := make([]domain.PlaygroundServiceInfo, 0, len(validation.Services))
	for _, summary := range validation.Services {
		services = append(services, domain.PlaygroundServiceInfo{
			Name:    summary.Name,
			Image:   summary.Image,
			Status:  "running",
			Health:  "healthy",
			Running: true,
		})
	}
	return services
}

// firstNonEmpty returns the first nonblank string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// writeRemote stores fake remote file content.
func (f *FakeExecutor) writeRemote(remotePath string, content string) {
	if f.ReadFiles == nil {
		f.ReadFiles = map[string]string{}
	}
	f.ReadFiles[remotePath] = content
}

// fakeFileInfo implements fs.FileInfo for fake remote files.
type fakeFileInfo struct {
	name string
	mode os.FileMode
}

// Name returns the fake entry name.
func (i fakeFileInfo) Name() string { return i.name }

// Size returns the fake entry size.
func (i fakeFileInfo) Size() int64 { return 0 }

// Mode returns the fake entry mode.
func (i fakeFileInfo) Mode() os.FileMode { return i.mode }

// ModTime returns the fake entry modification time.
func (i fakeFileInfo) ModTime() time.Time { return time.Time{} }

// IsDir reports whether the fake entry is a directory.
func (i fakeFileInfo) IsDir() bool { return i.mode.IsDir() }

// Sys returns no fake platform metadata.
func (i fakeFileInfo) Sys() any { return nil }

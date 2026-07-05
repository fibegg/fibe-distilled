package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// ErrRemoteFileMissing means the runtime filesystem was reachable but the requested file was absent.
var ErrRemoteFileMissing = errors.New("remote file missing")

// remoteFileReader reads runtime files for artifact drift checks.
type remoteFileReader interface {
	ReadFile(ctx context.Context, marquee domain.Marquee, remotePath string) (string, error)
}

// fsRemoteFileReader adapts RemoteFS into a runtime file reader.
type fsRemoteFileReader struct {
	fs RemoteFS
}

// runtimeArtifactExpectation contains the exact files that must exist on the runtime host.
type runtimeArtifactExpectation struct {
	Base         string
	ComposeYAML  string
	DockerConfig string
}

// runtimeArtifact is a single runtime file content expectation.
type runtimeArtifact struct {
	Name    string
	Path    string
	Content string
}

// remoteFileReaderFor uses typed runtime file reads.
func remoteFileReaderFor(fsys RemoteFS) remoteFileReader {
	return fsRemoteFileReader{fs: fsys}
}

// ReadFile reads a runtime file through RemoteFS.
func (r fsRemoteFileReader) ReadFile(ctx context.Context, marquee domain.Marquee, remotePath string) (string, error) {
	content, err := r.fs.ReadRemoteFile(ctx, marquee, remotePath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// checkRuntimeArtifactDrift compares runtime files with expected content.
func checkRuntimeArtifactDrift(ctx context.Context, reader remoteFileReader, marquee domain.Marquee, expected runtimeArtifactExpectation) (map[string]string, error) {
	drift := map[string]string{}
	for _, artifact := range expected.artifacts() {
		status, err := compareRemoteArtifact(ctx, reader, marquee, artifact)
		if err != nil {
			return nil, fmt.Errorf("runtime artifact drift check failed: %w", err)
		}
		if status != "" {
			drift[artifact.Name] = status
		}
	}
	return drift, nil
}

// artifacts returns the runtime files whose byte content must match rendering.
func (e runtimeArtifactExpectation) artifacts() []runtimeArtifact {
	return []runtimeArtifact{
		{Name: "compose", Path: e.Base + "/compose.yml", Content: e.ComposeYAML},
		{Name: "docker_config", Path: e.Base + "/docker-config/config.json", Content: e.DockerConfig},
	}
}

// compareRemoteArtifact reports missing or drifted content for one runtime file.
func compareRemoteArtifact(ctx context.Context, reader remoteFileReader, marquee domain.Marquee, artifact runtimeArtifact) (string, error) {
	actual, err := reader.ReadFile(ctx, marquee, artifact.Path)
	if errors.Is(err, ErrRemoteFileMissing) {
		return "missing", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", artifact.Name, err)
	}
	if actual != artifact.Content {
		return "drift", nil
	}
	return "", nil
}

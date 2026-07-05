package runtime

import (
	"errors"
	"io/fs"
	"path"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

const (
	// defaultBuildDockerfilePath is Docker's default Dockerfile path.
	defaultBuildDockerfilePath = "Dockerfile"
)

// RemoteCheckoutPath is an absolute runtime POSIX source checkout directory.
type RemoteCheckoutPath struct {
	value string
}

// NewRemoteCheckoutPath validates a checkout path under one Playground.
func NewRemoteCheckoutPath(project string, raw string) (RemoteCheckoutPath, error) {
	base, err := playgroundBase(project)
	if err != nil {
		return RemoteCheckoutPath{}, err
	}
	raw = strings.TrimSpace(raw)
	if !optfibe.ValidRemoteCheckoutPath(raw, base) {
		return RemoteCheckoutPath{}, errors.New("runtime checkout path must be an absolute checkout path under this playground")
	}
	return RemoteCheckoutPath{value: path.Clean(raw)}, nil
}

// String returns the runtime POSIX path for command generation.
func (p RemoteCheckoutPath) String() string {
	return p.value
}

// Parent returns the per-repository directory containing branch checkouts.
func (p RemoteCheckoutPath) Parent() string {
	return path.Dir(p.value)
}

// RelativeDockerfilePath is a POSIX Dockerfile path relative to one build context.
type RelativeDockerfilePath struct {
	value string
}

// NewRelativeDockerfilePath validates a Dockerfile path relative to a build context.
func NewRelativeDockerfilePath(raw string) (RelativeDockerfilePath, error) {
	raw = firstNonEmpty(raw, defaultBuildDockerfilePath)
	if !validRelativeBuildPath(raw) {
		return RelativeDockerfilePath{}, errors.New("dockerfile path must be relative and stay inside build context")
	}
	return RelativeDockerfilePath{value: path.Clean(raw)}, nil
}

// String returns the relative Dockerfile path for Docker CLI use.
func (p RelativeDockerfilePath) String() string {
	return p.value
}

// validRelativeBuildPath reports whether a build file path stays in its context.
func validRelativeBuildPath(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsRune(raw, '\x00') || strings.HasPrefix(raw, "/") {
		return false
	}
	if pathHasParentSegment(raw) {
		return false
	}
	clean := path.Clean(raw)
	return clean != "." && fs.ValidPath(clean)
}

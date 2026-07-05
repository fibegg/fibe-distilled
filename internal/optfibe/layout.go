package optfibe

import (
	"path"
	"strings"
)

const (
	// BuildsPath is the runtime directory for reusable Marquee build helpers.
	BuildsPath = "/opt/fibe/builds"
	// PlaygroundsPath is the root for per-Playground runtime trees.
	PlaygroundsPath = "/opt/fibe/playgrounds"
	// TraefikPath is the runtime directory for shared Marquee Traefik state.
	TraefikPath = "/opt/fibe/traefik"
)

// RuntimePrerequisiteDirs returns runtime directories required before deploy.
func RuntimePrerequisiteDirs() []string {
	return []string{PlaygroundsPath, TraefikPath, BuildsPath}
}

// PlaygroundPath returns the /opt/fibe path for a Compose project.
func PlaygroundPath(project string) string {
	return PlaygroundsPath + "/" + strings.TrimSpace(project)
}

// DockerConfigDir returns the Docker config directory for a Compose project.
func DockerConfigDir(project string) string {
	return PlaygroundPath(project) + "/docker-config"
}

// TraefikDockerConfigDir returns the Docker config directory for Traefik.
func TraefikDockerConfigDir() string {
	return TraefikPath + "/docker-config"
}

// TraefikACMEPath returns the ACME storage path for Traefik.
func TraefikACMEPath() string {
	return TraefikPath + "/acme.json"
}

// ValidRemoteCheckoutPath reports whether a checkout path stays inside one Playground.
func ValidRemoteCheckoutPath(raw string, playgroundBase string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsRune(raw, '\x00') || !path.IsAbs(raw) {
		return false
	}
	if pathHasParentSegment(raw) {
		return false
	}
	clean := path.Clean(raw)
	checkoutRoot := strings.TrimRight(playgroundBase, "/") + "/props/"
	return strings.HasPrefix(clean, checkoutRoot) && clean != strings.TrimSuffix(checkoutRoot, "/")
}

// pathHasParentSegment reports whether a slash path includes a parent segment.
func pathHasParentSegment(raw string) bool {
	for part := range strings.SplitSeq(raw, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

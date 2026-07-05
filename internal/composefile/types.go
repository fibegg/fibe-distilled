package composefile

import svc "github.com/fibegg/fibe-distilled/internal/composefile/service"

// document is the subset of a Compose file fibe-distilled reads and rewrites.
type document struct {
	// Services holds the named Compose services.
	Services map[string]svc.Definition `yaml:"services"`
	// Volumes holds top-level Compose volume definitions.
	Volumes map[string]any `yaml:"volumes,omitempty"`
	// Raw preserves top-level fields fibe-distilled does not interpret.
	Raw map[string]any `yaml:",inline"`
}

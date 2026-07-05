package service

// Definition is the subset of a Compose service definition fibe-distilled interprets.
type Definition struct {
	// Image is the image reference when the service is image-backed.
	Image string `yaml:"image,omitempty"`
	// Build is the raw Compose build declaration for dynamic image builds.
	Build any `yaml:"build,omitempty"`
	// Ports holds raw Compose port entries used only as local hints.
	Ports []any `yaml:"ports,omitempty"`
	// Labels holds raw service labels before Fibe-label normalization.
	Labels any `yaml:"labels,omitempty"`
	// Environment holds raw service environment entries before normalization.
	Environment any `yaml:"environment,omitempty"`
	// Restart holds the Compose restart policy when present.
	Restart any `yaml:"restart,omitempty"`
	// Raw preserves service fields fibe-distilled passes through.
	Raw map[string]any `yaml:",inline"`
}

// Summary is the normalized fibe-distilled view of one Compose service.
type Summary struct {
	// Name is the Compose service name.
	Name string `json:"name"`
	// Type is the fibe-distilled service classification.
	Type string `json:"type"`
	// Image is the effective service image reference.
	Image string `json:"image,omitempty"`
	// Build reports whether the service has dynamic build metadata.
	Build bool `json:"build,omitempty"`
	// Port is the routed HTTP container port from Fibe labels.
	Port int `json:"port,omitempty"`
	// Visibility is the Fibe route visibility value.
	Visibility string `json:"visibility,omitempty"`
	// Subdomain is the service subdomain below the Marquee root domain.
	Subdomain string `json:"subdomain,omitempty"`
	// RepoURL is the source repository URL from Fibe labels.
	RepoURL string `json:"repo_url,omitempty"`
	// Branch is the source branch selected for sync/build.
	Branch string `json:"branch,omitempty"`
	// Dockerfile is the service Dockerfile path for dynamic builds.
	Dockerfile string `json:"dockerfile,omitempty"`
	// SourceMount is the target path for synced source code.
	SourceMount string `json:"source_mount,omitempty"`
	// Production reports whether source mounts should be disabled for this service.
	Production bool `json:"production,omitempty"`
	// PathRule is the optional routed path predicate.
	PathRule string `json:"path_rule,omitempty"`
	// StartCommand is the runtime command override.
	StartCommand string `json:"start_command,omitempty"`
	// BuildTarget is the Docker build target.
	BuildTarget string `json:"build_target,omitempty"`
	// BuildArgs is the serialized build-args metadata.
	BuildArgs string `json:"build_args,omitempty"`
}

package service

import (
	"sort"
	"strconv"
	"strings"
)

const (
	// serviceTypeDynamic marks services with source/build metadata.
	serviceTypeDynamic = "dynamic"
	// serviceTypeStatic marks services that use an existing image only.
	serviceTypeStatic = "static"
	// defaultVisibility matches Fibe's public service default.
	defaultVisibility = "external"
	// fibeLabelBranch names the source branch label.
	fibeLabelBranch = "fibe.gg/branch"
	// fibeLabelBuildArgs names the Docker build args label.
	fibeLabelBuildArgs = "fibe.gg/build_args"
	// fibeLabelBuildTarget names the Docker build target label.
	fibeLabelBuildTarget = "fibe.gg/build_target"
	// fibeLabelDockerfile names the Dockerfile path label.
	fibeLabelDockerfile = "fibe.gg/dockerfile"
	// fibeLabelPathRule names the Traefik path rule label.
	fibeLabelPathRule = "fibe.gg/path_rule"
	// fibeLabelPort names the routed service port label.
	fibeLabelPort = "fibe.gg/port"
	// fibeLabelProduction names the source-mount production-mode label.
	fibeLabelProduction = "fibe.gg/production"
	// fibeLabelRepoURL names the repository URL label.
	fibeLabelRepoURL = "fibe.gg/repo_url"
	// fibeLabelSourceMount names the source mount target label.
	fibeLabelSourceMount = "fibe.gg/source_mount"
	// fibeLabelStartCmd names the start command label.
	fibeLabelStartCmd = "fibe.gg/start_command"
	// fibeLabelSubdomain names the service subdomain label.
	fibeLabelSubdomain = "fibe.gg/subdomain"
	// fibeLabelVisibility names the service visibility label.
	fibeLabelVisibility = "fibe.gg/visibility"
)

// Summaries returns deterministic service metadata extracted from Compose services.
func Summaries(services map[string]Definition) []Summary {
	names := sortedServiceNames(services)
	out := make([]Summary, 0, len(names))
	for _, name := range names {
		out = append(out, summarizeService(name, services[name]))
	}
	return out
}

// sortedServiceNames returns deterministic service iteration order.
func sortedServiceNames(services map[string]Definition) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// summarizeService extracts fibe-distilled metadata from one Compose service.
func summarizeService(name string, definition Definition) Summary {
	labels := NormalizeLabels(definition.Labels)
	summary := Summary{
		Name:         name,
		Type:         serviceType(definition, labels),
		Image:        definition.Image,
		Build:        serviceBuild(definition, labels),
		Port:         intLabel(labels, fibeLabelPort),
		Visibility:   label(labels, fibeLabelVisibility, defaultVisibility),
		Subdomain:    label(labels, fibeLabelSubdomain, name),
		RepoURL:      label(labels, fibeLabelRepoURL, ""),
		Branch:       label(labels, fibeLabelBranch, ""),
		Dockerfile:   label(labels, fibeLabelDockerfile, ""),
		SourceMount:  label(labels, fibeLabelSourceMount, ""),
		Production:   boolLabel(labels, fibeLabelProduction),
		PathRule:     label(labels, fibeLabelPathRule, ""),
		StartCommand: label(labels, fibeLabelStartCmd, ""),
		BuildTarget:  label(labels, fibeLabelBuildTarget, ""),
		BuildArgs:    label(labels, fibeLabelBuildArgs, ""),
	}
	return summary
}

// serviceType classifies a service as dynamic or static.
func serviceType(definition Definition, labels map[string]string) string {
	if label(labels, fibeLabelRepoURL, "") != "" || label(labels, fibeLabelSourceMount, "") != "" || definition.Build != nil {
		return serviceTypeDynamic
	}
	return serviceTypeStatic
}

// serviceBuild reports whether fibe-distilled should produce a dynamic image build.
func serviceBuild(definition Definition, labels map[string]string) bool {
	if definition.Build != nil {
		return true
	}
	return boolLabel(labels, fibeLabelProduction) && label(labels, fibeLabelRepoURL, "") != ""
}

// label returns a trimmed label value or fallback.
func label(labels map[string]string, key, fallback string) string {
	if v := strings.TrimSpace(labels[key]); v != "" {
		return v
	}
	return fallback
}

// intLabel parses an integer label with zero fallback.
func intLabel(labels map[string]string, key string) int {
	v := strings.TrimSpace(labels[key])
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

// boolLabel parses strict boolean label values with false fallback.
func boolLabel(labels map[string]string, key string) bool {
	parsed := parseStrictBool(labels[key])
	return parsed.ok && parsed.value
}

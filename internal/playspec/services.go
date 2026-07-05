package playspec

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	servicepkg "github.com/fibegg/fibe-distilled/internal/composefile/service"
)

// playspecServiceOverrideKeys are service metadata keys mirrored into labels.
var playspecServiceOverrideKeys = []string{
	"repo_url",
	"dockerfile_path",
	"start_command",
	"build_target",
	"build_args",
	"production",
	"image",
	"exposure",
}

// playspecServiceMetadataFields are accepted service metadata keys.
var playspecServiceMetadataFields = map[string]bool{
	"name":            true,
	"type":            true,
	"repo_url":        true,
	"dockerfile_path": true,
	"start_command":   true,
	"build_target":    true,
	"build_args":      true,
	"production":      true,
	"image":           true,
	"exposure":        true,
}

// ApplyPlayspecServices applies Playspec service metadata to a base Compose file.
func ApplyPlayspecServices(composeYAML string, services []any) (string, error) {
	if len(services) == 0 {
		return composeYAML, nil
	}
	rendered, err := compose.MutationMap(composeYAML)
	if err != nil {
		return "", err
	}
	composeServices, ok := compose.AsMap(rendered["services"])
	if !ok {
		return "", errors.New("services metadata require compose services")
	}
	overrides, err := playspecServiceOverrides(services, composeServices)
	if err != nil {
		return "", err
	}
	if err := servicepkg.ApplyServiceOverrides(rendered, overrides); err != nil {
		return "", err
	}
	return compose.MutationYAML(rendered)
}

// playspecServiceOverrides validates and converts Playspec service metadata.
func playspecServiceOverrides(services []any, composeServices map[string]any) (map[string]any, error) {
	overrides := map[string]any{}
	seen := map[string]bool{}
	for idx, raw := range services {
		metadata, err := parsePlayspecServiceMetadata(idx, raw)
		if err != nil {
			return nil, err
		}
		if err := validatePlayspecServiceMetadata(idx, metadata.name, metadata.service, seen, composeServices); err != nil {
			return nil, err
		}
		override := playspecServiceOverride(metadata.service)
		if len(override) > 0 {
			overrides[metadata.name] = override
		}
	}
	return overrides, nil
}

// playspecServiceMetadata carries one parsed services[] metadata item.
type playspecServiceMetadata struct {
	service map[string]any
	name    string
}

// parsePlayspecServiceMetadata reads one service metadata object and name.
func parsePlayspecServiceMetadata(idx int, raw any) (playspecServiceMetadata, error) {
	service, ok := compose.AsMap(raw)
	if !ok {
		return playspecServiceMetadata{}, fmt.Errorf("services[%d] must be an object", idx)
	}
	rawName, ok := service["name"]
	if !ok || rawName == nil {
		return playspecServiceMetadata{}, fmt.Errorf("services[%d].name is required", idx)
	}
	nameValue, ok := rawName.(string)
	if !ok {
		return playspecServiceMetadata{}, fmt.Errorf("services[%d].name must be a string", idx)
	}
	name := strings.TrimSpace(nameValue)
	if name == "" {
		return playspecServiceMetadata{}, fmt.Errorf("services[%d].name is required", idx)
	}
	return playspecServiceMetadata{service: service, name: name}, nil
}

// validatePlayspecServiceMetadata checks service metadata against Compose.
func validatePlayspecServiceMetadata(idx int, name string, service map[string]any, seen map[string]bool, composeServices map[string]any) error {
	if seen[name] {
		return fmt.Errorf("services[%d].name duplicates %q", idx, name)
	}
	seen[name] = true
	if _, ok := composeServices[name]; !ok {
		return fmt.Errorf("services[%d].name %q is not present in compose", idx, name)
	}
	rawType, hasType := service["type"]
	if err := validatePlayspecServiceType(idx, rawType, hasType); err != nil {
		return err
	}
	if _, ok := service["job_watch"]; ok {
		return fmt.Errorf("services[%d].job_watch is not implemented in fibe-distilled", idx)
	}
	if err := validatePlayspecServiceMetadataContent(idx, service); err != nil {
		return err
	}
	return nil
}

// validatePlayspecServiceType checks the supported static/dynamic vocabulary.
func validatePlayspecServiceType(idx int, rawType any, present bool) error {
	if !present {
		return nil
	}
	serviceType, ok := rawType.(string)
	if !ok {
		return fmt.Errorf("services[%d].type must be a string", idx)
	}
	switch strings.TrimSpace(serviceType) {
	case "static", "dynamic":
		return nil
	case "":
		return fmt.Errorf("services[%d].type is required when present", idx)
	default:
		return fmt.Errorf("services[%d].type must be static or dynamic", idx)
	}
}

// playspecServiceOverride extracts supported override fields from metadata.
func playspecServiceOverride(service map[string]any) map[string]any {
	override := map[string]any{}
	for _, key := range playspecServiceOverrideKeys {
		if value, ok := service[key]; ok {
			override[key] = value
		}
	}
	return override
}

// validatePlayspecServiceMetadataContent rejects ignored metadata fields.
func validatePlayspecServiceMetadataContent(idx int, service map[string]any) error {
	var issues []string
	for key := range service {
		if !playspecServiceMetadataFields[key] {
			issues = append(issues, fmt.Sprintf("services[%d].%s", idx, key))
		}
	}
	override := playspecServiceOverride(service)
	issues = append(issues, servicepkg.OverrideContentIssuePaths(fmt.Sprintf("services[%d]", idx), override)...)
	if len(issues) == 0 {
		return nil
	}
	sort.Strings(issues)
	return fmt.Errorf("services[%d] has unsupported or malformed metadata fields: %s", idx, strings.Join(issues, ", "))
}

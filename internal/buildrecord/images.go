package buildrecord

import (
	"fmt"
	"strings"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
)

// ApplyBuildImages replaces built services with deterministic image references.
func ApplyBuildImages(composeYAML string, imageRefs map[string]string) (string, error) {
	if len(imageRefs) == 0 {
		return composeYAML, nil
	}
	rendered, err := compose.MutationMap(composeYAML)
	if err != nil {
		return "", err
	}
	servicesRaw, _ := compose.AsMap(rendered["services"])
	for name, imageRef := range imageRefs {
		raw, ok := servicesRaw[name].(map[string]any)
		if !ok {
			return "", fmt.Errorf("built service %q missing from runtime compose", name)
		}
		if strings.TrimSpace(imageRef) == "" {
			return "", fmt.Errorf("built service %q has empty image ref", name)
		}
		delete(raw, "build")
		raw["image"] = imageRef
		raw["pull_policy"] = "never"
	}
	return compose.MutationYAML(rendered)
}

package playground

import (
	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
)

// ApplyOverrides applies Playground-specific env and service overrides.
func ApplyOverrides(composeYAML string, globalEnv map[string]string, serviceOverrides map[string]any) (string, error) {
	if len(globalEnv) == 0 && len(serviceOverrides) == 0 {
		return composeYAML, nil
	}
	if err := service.RejectJobModeServiceOverrides(serviceOverrides); err != nil {
		return "", err
	}
	rendered, err := compose.MutationMap(composeYAML)
	if err != nil {
		return "", err
	}
	if err := service.ValidateServiceOverrideNames(rendered, nil, serviceOverrides); err != nil {
		return "", err
	}
	if err := service.ApplyGlobalEnv(rendered, globalEnv); err != nil {
		return "", err
	}
	if err := service.ApplyServiceOverrides(rendered, serviceOverrides); err != nil {
		return "", err
	}
	return compose.MutationYAML(rendered)
}

package launch

import (
	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	fibetemplate "github.com/fibegg/fibe-distilled/internal/composefile/template"
)

// OverrideOptions carries context needed by launch-time template expansion.
type OverrideOptions struct {
	// RootDomain is substituted into supported Fibe template tokens.
	RootDomain string
	// RandomBytes overrides random template entropy; nil uses crypto/rand.
	RandomBytes func([]byte) (int, error)
}

// ApplyOverrides applies CLI launch variables, service subdomains, service
// overrides, and supported template tokens.
func ApplyOverrides(composeYAML string, variables map[string]string, serviceSubdomains map[string]string, serviceOverrides map[string]any, options OverrideOptions) (string, error) {
	if err := service.RejectJobModeServiceOverrides(serviceOverrides); err != nil {
		return "", err
	}
	rendered, err := compose.MutationMap(composeYAML)
	if err != nil {
		return "", err
	}
	if err := fibetemplate.ApplyVariables(rendered, variables, options.RandomBytes); err != nil {
		return "", err
	}
	if err := fibetemplate.ApplyRootDomainToken(rendered, options.RootDomain); err != nil {
		return "", err
	}
	if err := service.ValidateServiceOverrideNames(rendered, serviceSubdomains, serviceOverrides); err != nil {
		return "", err
	}
	if err := service.ApplyServiceSubdomains(rendered, serviceSubdomains); err != nil {
		return "", err
	}
	if err := service.ApplyServiceOverrides(rendered, serviceOverrides); err != nil {
		return "", err
	}
	return compose.MutationYAML(rendered)
}

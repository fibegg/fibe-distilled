package composefile

import (
	"errors"
	"fmt"
	"path"
	"strings"

	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	fibetemplate "github.com/fibegg/fibe-distilled/internal/composefile/template"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"gopkg.in/yaml.v3"
)

// RuntimeOptions carries deployment-specific data used while rendering runtime Compose.
type RuntimeOptions = service.RuntimeOptions

// RuntimeResult carries rendered deployment Compose and projected service metadata.
type RuntimeResult struct {
	// ComposeYAML is the Compose document to write to the runtime host.
	ComposeYAML string
	// Services are the initial service status records exposed on the Playground.
	Services []domain.PlaygroundServiceInfo
	// ServiceURLs are the initial routed service URL records.
	ServiceURLs []domain.PlaygroundServiceURL
}

// RuntimeWithOptions renders the Compose YAML that is actually deployed.
func RuntimeWithOptions(composeYAML string, project string, rootDomain string, scheme string, options RuntimeOptions) (RuntimeResult, error) {
	if fibetemplate.HasUnresolvedTokens(composeYAML) {
		return RuntimeResult{}, errors.New("compose contains unresolved Fibe template variables; launch with variables before deploying a Playground")
	}
	doc, err := parseDocument(composeYAML)
	if err != nil {
		return RuntimeResult{}, err
	}
	summaries := service.Summaries(doc.Services)
	if errs := serviceLabelValidationErrors(doc, summaries); len(errs) > 0 {
		return RuntimeResult{}, errors.New(strings.Join(errs, "; "))
	}
	var rendered map[string]any
	if err := yaml.Unmarshal([]byte(composeYAML), &rendered); err != nil {
		return RuntimeResult{}, err
	}
	servicesRaw, _ := rendered["services"].(map[string]any)
	sourcePaths := service.RuntimeSourcePaths(summaries, project)
	if err := rewriteRelativeConfigFiles(rendered, sourcePaths); err != nil {
		return RuntimeResult{}, err
	}
	metadata, err := service.RenderRuntimeServices(servicesRaw, summaries, project, rootDomain, scheme, options)
	if err != nil {
		return RuntimeResult{}, err
	}
	service.InjectSourcePathVariables(servicesRaw, sourcePaths)
	out, err := yaml.Marshal(rendered)
	if err != nil {
		return RuntimeResult{}, err
	}
	return RuntimeResult{
		ComposeYAML: string(out),
		Services:    metadata.Services,
		ServiceURLs: metadata.ServiceURLs,
	}, nil
}

// rewriteRelativeConfigFiles points Compose config files at the synced checkout.
func rewriteRelativeConfigFiles(rendered map[string]any, sourcePaths map[string]string) error {
	configs := configDefinitions(rendered)
	if len(configs) == 0 {
		return nil
	}
	sourceRoot, sourceCount := singleSourceRoot(sourcePaths)
	for name, raw := range configs {
		definition, configFile, ok, err := relativeConfigFile(name, raw)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if sourceCount != 1 {
			return fmt.Errorf("config %q uses relative file %q but runtime compose has %d synced source checkouts", name, configFile, sourceCount)
		}
		definition["file"] = path.Join(sourceRoot, path.Clean(configFile))
	}
	return nil
}

// configDefinitions returns top-level Compose config definitions.
func configDefinitions(rendered map[string]any) map[string]any {
	configs, ok := rendered["configs"].(map[string]any)
	if !ok {
		return nil
	}
	return configs
}

// relativeConfigFile returns a relative config file path when one is present.
func relativeConfigFile(name string, raw any) (map[string]any, string, bool, error) {
	definition, ok := raw.(map[string]any)
	if !ok {
		return nil, "", false, nil
	}
	rawFile, ok := definition["file"]
	if !ok {
		return nil, "", false, nil
	}
	configFile, ok := rawFile.(string)
	if !ok {
		return nil, "", false, nil
	}
	configFile = strings.TrimSpace(configFile)
	if configFile == "" || path.IsAbs(configFile) {
		return nil, "", false, nil
	}
	if err := validateRelativeConfigFile(name, configFile); err != nil {
		return nil, "", false, err
	}
	return definition, configFile, true, nil
}

// validateRelativeConfigFile rejects paths that would leave the source checkout.
func validateRelativeConfigFile(name string, configFile string) error {
	cleaned := path.Clean(configFile)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("config %q file must stay inside the synced source checkout", name)
	}
	return nil
}

// singleSourceRoot returns the only source checkout and the total source count.
func singleSourceRoot(sourcePaths map[string]string) (string, int) {
	var root string
	for _, sourcePath := range sourcePaths {
		root = sourcePath
	}
	return root, len(sourcePaths)
}

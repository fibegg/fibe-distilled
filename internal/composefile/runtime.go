package composefile

import (
	"errors"
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

package service

import (
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// RuntimeOptions carries deployment-specific data used while rendering runtime services.
type RuntimeOptions struct {
	// InternalPassword is the default Basic Auth password for internal routes.
	InternalPassword string
	// PlaygroundID is written into generated route labels.
	PlaygroundID int64
	// ServicePasswords overrides Basic Auth passwords per service.
	ServicePasswords map[string]string
}

// RuntimeResult carries projected service metadata.
type RuntimeResult struct {
	// Services are the initial service status records exposed on the Playground.
	Services []domain.PlaygroundServiceInfo
	// ServiceURLs are the initial routed service URL records.
	ServiceURLs []domain.PlaygroundServiceURL
}

// RuntimeSourcePaths maps services to remote source checkout paths.
func RuntimeSourcePaths(summaries []Summary, project string) map[string]string {
	sourcePaths := map[string]string{}
	for _, summary := range summaries {
		if summary.RepoURL != "" && summary.SourceMount != "" && !summary.Production {
			sourcePaths[summary.Name] = sourcePath(summary, project)
		}
	}
	return sourcePaths
}

// RenderRuntimeServices applies runtime mutations for all summarized services.
func RenderRuntimeServices(services map[string]any, summaries []Summary, project string, rootDomain string, scheme string, options RuntimeOptions) (RuntimeResult, error) {
	infos := make([]domain.PlaygroundServiceInfo, 0, len(summaries))
	urls := make([]domain.PlaygroundServiceURL, 0, len(summaries))
	for _, summary := range summaries {
		infos = append(infos, runtimeServiceInfo(summary))
		route, err := renderRuntimeService(services, summary, project, rootDomain, scheme, options)
		if err != nil {
			return RuntimeResult{}, err
		}
		if route.routed {
			urls = append(urls, route.url)
		}
	}
	return RuntimeResult{Services: infos, ServiceURLs: urls}, nil
}

// runtimeRouteResult carries one service route and whether it was generated.
type runtimeRouteResult struct {
	url    domain.PlaygroundServiceURL
	routed bool
}

// renderRuntimeService applies runtime mutations for one Compose service.
func renderRuntimeService(services map[string]any, summary Summary, project string, rootDomain string, scheme string, options RuntimeOptions) (runtimeRouteResult, error) {
	stripHostPorts(services, summary.Name)
	route, err := renderRuntimeRoute(services, summary, project, rootDomain, scheme, options)
	if err != nil {
		return runtimeRouteResult{}, err
	}
	injectStartCommand(services, summary)
	injectSourcePath(services, summary, project)
	return route, nil
}

// renderRuntimeRoute injects Traefik labels and returns initial URL state.
func renderRuntimeRoute(services map[string]any, summary Summary, project string, rootDomain string, scheme string, options RuntimeOptions) (runtimeRouteResult, error) {
	if summary.Port == 0 {
		return runtimeRouteResult{}, nil
	}
	disableDockerHealthcheck(services, summary.Name)
	password := runtimeServicePassword(summary.Name, options)
	if err := injectTraefik(services, summary, project, rootDomain, scheme, password, options.PlaygroundID); err != nil {
		return runtimeRouteResult{}, err
	}
	return runtimeRouteResult{url: runtimeServiceURL(summary, rootDomain, scheme), routed: true}, nil
}

// stripHostPorts removes raw host ports so Traefik owns routed exposure.
func stripHostPorts(services map[string]any, name string) {
	service, ok := services[name].(map[string]any)
	if !ok {
		return
	}
	delete(service, "ports")
}

// disableDockerHealthcheck keeps routed services visible to Traefik while starting.
func disableDockerHealthcheck(services map[string]any, name string) {
	service, ok := services[name].(map[string]any)
	if !ok {
		return
	}
	service["healthcheck"] = map[string]any{"disable": true}
}

// runtimeServiceInfo returns the initial service state exposed by the API.
func runtimeServiceInfo(summary Summary) domain.PlaygroundServiceInfo {
	return domain.PlaygroundServiceInfo{
		Name:    summary.Name,
		Status:  "pending",
		Image:   summary.Image,
		Health:  "unknown",
		Running: false,
	}
}

// runtimeServiceURL returns the initial routed service URL state.
func runtimeServiceURL(summary Summary, rootDomain string, scheme string) domain.PlaygroundServiceURL {
	running := false
	return domain.PlaygroundServiceURL{
		Name:         summary.Name,
		Type:         "http",
		URL:          serviceURL(scheme, rootDomain, summary.Subdomain),
		Visibility:   summary.Visibility,
		AuthRequired: summary.Visibility == "internal",
		Status:       "pending",
		Health:       "unknown",
		Running:      &running,
	}
}

// runtimeServicePassword selects per-service auth or the Playground secret.
func runtimeServicePassword(serviceName string, options RuntimeOptions) string {
	if override := strings.TrimSpace(options.ServicePasswords[serviceName]); override != "" {
		return override
	}
	return options.InternalPassword
}

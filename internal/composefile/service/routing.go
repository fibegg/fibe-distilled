package service

import (
	"strings"
)

// injectTraefik adds Traefik labels for one routed service.
func injectTraefik(services map[string]any, summary Summary, project string, rootDomain string, scheme string, internalPassword string, playgroundID int64) error {
	raw, ok := routedService(services, summary, rootDomain)
	if !ok {
		return nil
	}
	labels := normalizeLabelsAny(raw["labels"])
	router := project + "-" + summary.Name
	host := hostRule(rootDomain, summary.Subdomain)
	addHTTPRouterLabels(labels, router, project, routeRule(host, summary.PathRule), summary.Port)
	if scheme == "https" {
		addHTTPSRouterLabels(labels, router, routeRule(host, summary.PathRule))
	}
	if err := addInternalRouteAuth(labels, router, scheme, summary.Visibility, internalPassword, playgroundID); err != nil {
		return err
	}
	raw["labels"] = labels
	delete(raw, "ports")
	return nil
}

// routedService returns a mutable service only when routing is possible.
func routedService(services map[string]any, summary Summary, rootDomain string) (map[string]any, bool) {
	if rootDomain == "" || summary.Port == 0 {
		return nil, false
	}
	raw, ok := services[summary.Name].(map[string]any)
	return raw, ok
}

// routeRule combines the generated host rule with an optional path rule.
func routeRule(host string, pathRule string) string {
	rule := "Host(`" + host + "`)"
	if strings.TrimSpace(pathRule) != "" {
		rule += " && (" + pathRule + ")"
	}
	return rule
}

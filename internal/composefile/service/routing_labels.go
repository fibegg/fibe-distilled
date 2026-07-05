package service

import "strconv"

const (
	// traefikRouterPrefix prefixes generated router label keys.
	traefikRouterPrefix = "traefik.http.routers."
	// traefikServicePrefix prefixes generated service label keys.
	traefikServicePrefix = "traefik.http.services."
	// fibeDistilledManagedLabel scopes the managed Traefik Docker provider.
	fibeDistilledManagedLabel = "fibe-distilled.managed"
)

// addHTTPRouterLabels adds the baseline HTTP router and service labels.
func addHTTPRouterLabels(labels map[string]any, router string, project string, rule string, port int) {
	labels[fibeDistilledManagedLabel] = "true"
	labels["traefik.enable"] = "true"
	labels["traefik.docker.network"] = project + "_default"
	labels[traefikRouterPrefix+router+"-http.rule"] = rule
	labels[traefikRouterPrefix+router+"-http.entrypoints"] = "web"
	labels[traefikRouterPrefix+router+"-http.service"] = router
	labels[traefikServicePrefix+router+".loadbalancer.server.port"] = strconv.Itoa(port)
}

// addHTTPSRouterLabels adds HTTPS router and HTTP redirect labels.
func addHTTPSRouterLabels(labels map[string]any, router string, rule string) {
	redirectMiddleware := router + "-redirect"
	labels["traefik.http.middlewares."+redirectMiddleware+".redirectscheme.scheme"] = "https"
	labels[traefikRouterPrefix+router+"-http.middlewares"] = appendMiddleware(labels[traefikRouterPrefix+router+"-http.middlewares"], redirectMiddleware)
	labels[traefikRouterPrefix+router+"-secure.rule"] = rule
	labels[traefikRouterPrefix+router+"-secure.entrypoints"] = "websecure"
	labels[traefikRouterPrefix+router+"-secure.tls"] = "true"
	labels[traefikRouterPrefix+router+"-secure.tls.certresolver"] = "letsencrypt"
	labels[traefikRouterPrefix+router+"-secure.service"] = router
}

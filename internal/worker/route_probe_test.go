package worker

import (
	"net/http"
	"testing"
)

func TestRouteProbeResponseReadyRejectsTraefikDefaultNotFound(t *testing.T) {
	if routeProbeResponseReady(http.StatusNotFound, []byte("404 page not found\n")) {
		t.Fatalf("expected Traefik default 404 to be treated as not ready")
	}
	if !routeProbeResponseReady(http.StatusNotFound, []byte("app-specific 404 page")) {
		t.Fatalf("expected app-level 404 to prove the route reached the service")
	}
	if routeProbeResponseReady(http.StatusBadGateway, []byte("")) {
		t.Fatalf("expected 502 to be treated as not ready")
	}
	if !routeProbeResponseReady(http.StatusUnauthorized, []byte("")) {
		t.Fatalf("expected auth challenge to prove the route reached the service")
	}
}

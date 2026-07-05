package worker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultRouteProbeTimeout caps one public route readiness request.
const defaultRouteProbeTimeout = 5 * time.Second

// maxRouteProbeBodyBytes bounds body reads used to identify proxy default pages.
const maxRouteProbeBodyBytes = 4096

// routeProbeReady reports whether a routed service URL reaches the app instead
// of Traefik's not-found or backend-unavailable responses. Auth failures and
// app-level 404s still prove Traefik reached the service.
func (w Worker) routeProbeReady(ctx context.Context, rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "fibe-distilled-runtime-probe")
	resp, err := w.routeProbeClient().Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRouteProbeBodyBytes))
	return routeProbeResponseReady(resp.StatusCode, body)
}

// routeProbeResponseReady reports whether an HTTP response proves app reachability.
func routeProbeResponseReady(statusCode int, body []byte) bool {
	if statusCode <= 0 || statusCode >= http.StatusInternalServerError {
		return false
	}
	if statusCode == http.StatusNotFound && routeProbeDefaultNotFound(body) {
		return false
	}
	return true
}

// routeProbeDefaultNotFound reports Traefik's built-in not-found response.
func routeProbeDefaultNotFound(body []byte) bool {
	return strings.TrimSpace(string(body)) == "404 page not found"
}

// routeProbeClient returns the configured probe client or a short-timeout default.
func (w Worker) routeProbeClient() HTTPDoer {
	if w.RouteProbeClient != nil {
		return w.RouteProbeClient
	}
	return &http.Client{Timeout: defaultRouteProbeTimeout}
}

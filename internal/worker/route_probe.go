package worker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultRouteProbeTimeout = 5 * time.Second

// routeProbeReady reports whether a routed service URL reaches any non-5xx
// response. Auth failures and app-level 404s still prove Traefik reached the
// service instead of its backend being unavailable.
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
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode > 0 && resp.StatusCode < http.StatusInternalServerError
}

// routeProbeClient returns the configured probe client or a short-timeout default.
func (w Worker) routeProbeClient() HTTPDoer {
	if w.RouteProbeClient != nil {
		return w.RouteProbeClient
	}
	return &http.Client{Timeout: defaultRouteProbeTimeout}
}

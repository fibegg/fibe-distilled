package playground

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// playgroundsLogs queues live or cached Playground log collection.
func (h Handler) playgroundsLogs(w http.ResponseWriter, r *http.Request) {
	pg, ok := h.loadPlayground(w, r)
	if !ok {
		return
	}
	var body playgroundLogsPayload
	if !decodeOptional(w, r, &body) {
		return
	}
	options, err := playgroundLogsOptions(body)
	if err != nil {
		writePayloadErr(w, r, err)
		return
	}
	logTarget, ok := h.playgroundLogsTarget(w, r, pg, options.service)
	if !ok {
		return
	}
	op, err := h.services.Enqueue(r.Context(), func(ctx context.Context) (map[string]any, *domain.APIError) {
		return h.playgroundLogsPayload(ctx, logTarget, options.service, options.tail)
	})
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusAccepted, map[string]any{"request_id": op.ID, "status": "queued", "status_url": op.StatusURL})
}

// playgroundLogsTarget reloads a Playground and validates the requested service.
func (h Handler) playgroundLogsTarget(w http.ResponseWriter, r *http.Request, loaded domain.Playground, service string) (domain.Playground, bool) {
	if service == "" {
		return loaded, true
	}
	current, err := h.repo.GetPlayground(r.Context(), idString(loaded.ID))
	if err != nil {
		writeStoreErr(w, r, "playground", err)
		return loaded, false
	}
	if !playgroundHasService(current, service) {
		response.Error(w, r, http.StatusUnprocessableEntity, "SERVICE_NOT_FOUND", "service "+service+" not found", nil)
		return current, false
	}
	return current, true
}

// playgroundLogOptions carries validated log request options.
type playgroundLogOptions struct {
	service string
	tail    int
}

// playgroundLogsOptions validates service/tail fields and applies defaults.
func playgroundLogsOptions(body playgroundLogsPayload) (playgroundLogOptions, error) {
	if presentBlankStringPtr(body.fields, "service", body.Service) {
		return playgroundLogOptions{}, badRequestError{message: "service must not be blank"}
	}
	if body.fields.Has("tail") && body.Tail == nil {
		return playgroundLogOptions{}, badRequestError{message: "tail must be a non-negative integer"}
	}
	tail := 5000
	if body.Tail != nil {
		tail = *body.Tail
	}
	if tail < 0 {
		return playgroundLogOptions{}, badRequestError{message: "tail must be a non-negative integer"}
	}
	return playgroundLogOptions{service: body.service(), tail: tail}, nil
}

// playgroundLogsPayload builds the async log response payload.
func (h Handler) playgroundLogsPayload(ctx context.Context, pg domain.Playground, service string, tail int) (map[string]any, *domain.APIError) {
	current, apiErr := h.currentPlaygroundForLogs(ctx, pg.ID)
	if apiErr != nil {
		return nil, apiErr
	}
	if service != "" && !playgroundHasService(current, service) {
		return nil, &domain.APIError{Code: "SERVICE_NOT_FOUND", Message: "service " + service + " not found"}
	}
	if service == "" {
		return h.allPlaygroundLogsPayload(ctx, current, tail)
	}
	return h.singlePlaygroundLogsPayload(ctx, current, service, tail)
}

// LogsPayload returns the async log payload for neighboring package tests and services.
func (h Handler) LogsPayload(ctx context.Context, pg domain.Playground, service string, tail int) (map[string]any, *domain.APIError) {
	return h.playgroundLogsPayload(ctx, pg, service, tail)
}

// currentPlaygroundForLogs reloads a Playground for async log work.
func (h Handler) currentPlaygroundForLogs(ctx context.Context, id int64) (domain.Playground, *domain.APIError) {
	pg, err := h.repo.GetPlayground(ctx, idString(id))
	if err == nil {
		return pg, nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return domain.Playground{}, &domain.APIError{
			Code:    "RESOURCE_NOT_FOUND",
			Message: "playground not found",
			Details: map[string]any{"resource": "playground", "id": id},
		}
	}
	return domain.Playground{}, &domain.APIError{Code: "INTERNAL_ERROR", Message: err.Error()}
}

// singlePlaygroundLogsPayload returns logs for one service.
func (h Handler) singlePlaygroundLogsPayload(ctx context.Context, pg domain.Playground, service string, tail int) (map[string]any, *domain.APIError) {
	if pg.MarqueeID != nil && pg.ComposeProject != nil {
		lines, apiErr := h.liveServiceLogLines(ctx, pg, service, tail)
		if apiErr != nil {
			return nil, apiErr
		}
		return serviceLogPayload(pg.ID, service, lines, "docker"), nil
	}
	return serviceLogPayload(pg.ID, service, []string{}, "cached"), nil
}

// allPlaygroundLogsPayload aggregates logs for all known services.
func (h Handler) allPlaygroundLogsPayload(ctx context.Context, pg domain.Playground, tail int) (map[string]any, *domain.APIError) {
	payloads := []map[string]any{}
	if pg.MarqueeID != nil && pg.ComposeProject != nil {
		for _, service := range playgroundLogServiceNames(pg) {
			lines, apiErr := h.liveServiceLogLines(ctx, pg, service, tail)
			if apiErr != nil {
				return nil, apiErr
			}
			payloads = append(payloads, serviceLogPayload(pg.ID, service, lines, "docker"))
		}
	}
	return aggregateLogPayload(pg.ID, payloads), nil
}

// liveServiceLogLines reads service logs from the local Compose runtime.
func (h Handler) liveServiceLogLines(ctx context.Context, pg domain.Playground, service string, tail int) ([]string, *domain.APIError) {
	mq, found, err := h.repo.GetRuntimeMarquee(ctx)
	if !found || errors.Is(err, store.ErrNotFound) {
		return nil, &domain.APIError{
			Code:    "LOGS_UNAVAILABLE",
			Message: fmt.Sprintf("playground references missing marquee %d", *pg.MarqueeID),
			Details: map[string]any{"dependency": "marquee", "id": *pg.MarqueeID},
		}
	}
	if err != nil {
		return nil, &domain.APIError{Code: "INTERNAL_ERROR", Message: err.Error()}
	}
	lines, err := h.runtime.Logs(ctx, mq, *pg.ComposeProject, service, tail)
	if err != nil {
		return nil, &domain.APIError{
			Code:    "LOGS_UNAVAILABLE",
			Message: err.Error(),
			Details: map[string]any{"compose_project": *pg.ComposeProject, "service": service},
		}
	}
	return lines, nil
}

// serviceLogPayload builds the SDK log payload for one service.
func serviceLogPayload(playgroundID int64, service string, lines []string, source string) map[string]any {
	return map[string]any{
		"playground_id": playgroundID,
		"service":       service,
		"lines":         lines,
		"source":        source,
	}
}

// aggregateLogPayload combines service logs into a single response.
func aggregateLogPayload(playgroundID int64, payloads []map[string]any) map[string]any {
	lines := []string{}
	entries := []map[string]any{}
	diagnostics := map[string]any{}
	sources := map[string]bool{}
	for _, payload := range payloads {
		service, _ := payload["service"].(string)
		source, _ := payload["source"].(string)
		if source != "" {
			sources[source] = true
		}
		diagnostics[service] = map[string]any{"source": source}
		for _, line := range stringSlice(payload["lines"]) {
			lines = append(lines, "["+service+"] "+line)
			entries = append(entries, map[string]any{"service": service, "line": line, "source": source})
		}
	}
	return map[string]any{
		"playground_id": playgroundID,
		"service":       "",
		"lines":         lines,
		"source":        aggregateLogSource(sources),
		"entries":       entries,
		"diagnostics":   map[string]any{"services": diagnostics},
	}
}

// aggregateLogSource summarizes whether aggregate logs came from live sources.
func aggregateLogSource(sources map[string]bool) string {
	if len(sources) == 0 {
		return "none"
	}
	var only string
	for source := range sources {
		if only != "" {
			return "mixed"
		}
		only = source
	}
	return only
}

// playgroundLogServiceNames returns known service names from service state and URLs.
func playgroundLogServiceNames(pg domain.Playground) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(pg.Services)+len(pg.ServiceURLs))
	for _, service := range pg.Services {
		names = appendServiceName(names, seen, service.Name)
	}
	for _, serviceURL := range pg.ServiceURLs {
		names = appendServiceName(names, seen, serviceURL.Name)
	}
	sort.Strings(names)
	return names
}

// appendServiceName appends a trimmed service name once.
func appendServiceName(names []string, seen map[string]bool, raw string) []string {
	name := strings.TrimSpace(raw)
	if name == "" || seen[name] {
		return names
	}
	seen[name] = true
	return append(names, name)
}

// stringSlice type-checks a payload value as []string.
func stringSlice(value any) []string {
	if typed, ok := value.([]string); ok {
		return typed
	}
	return []string{}
}

// playgroundHasService reports whether a Playground exposes a service name.
func playgroundHasService(pg domain.Playground, service string) bool {
	return slices.Contains(playgroundLogServiceNames(pg), service)
}

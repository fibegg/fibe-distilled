package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// ComposeServiceState mirrors one docker compose ps JSON object.
type ComposeServiceState struct {
	// Service is the Compose service name.
	Service string `json:"Service"`
	// Name is the container name fallback used by some Compose versions.
	Name string `json:"Name"`
	// Image is the container image reference.
	Image string `json:"Image"`
	// State is the Docker container state.
	State string `json:"State"`
	// Health is the Docker health status when present.
	Health string `json:"Health"`
	// ExitCode is the container exit code for terminal states.
	ExitCode int `json:"ExitCode"`
}

// InspectServices reads docker compose service state for one project.
func (c Checker) InspectServices(ctx context.Context, marquee domain.Marquee, project string) ([]domain.PlaygroundServiceInfo, error) {
	base, composeYAML, err := c.projectComposeYAML(ctx, marquee, project)
	if err != nil {
		return nil, err
	}
	services, err := c.composeRuntime().Services(ctx, marquee, project, base, composeYAML)
	if err != nil {
		return nil, fmt.Errorf("inspect compose services failed: %w", err)
	}
	return services, nil
}

// ParseComposeServiceStates accepts array or line-delimited Compose JSON.
func ParseComposeServiceStates(raw string) ([]ComposeServiceState, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		return parseComposeServiceStateArray(raw)
	}
	return parseComposeServiceStateLines(raw)
}

// parseComposeServiceStateArray decodes array-form Compose service JSON.
func parseComposeServiceStateArray(raw string) ([]ComposeServiceState, error) {
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("parse docker compose ps JSON array: %w", err)
	}
	out := make([]ComposeServiceState, 0, len(items))
	for index, item := range items {
		state, err := parseComposeServiceState(item, fmt.Sprintf("array item %d", index+1))
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}

// parseComposeServiceStateLines decodes line-delimited Compose service JSON.
func parseComposeServiceStateLines(raw string) ([]ComposeServiceState, error) {
	var out []ComposeServiceState
	for index, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		state, err := parseComposeServiceState(json.RawMessage(line), fmt.Sprintf("line %d", index+1))
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}

// parseComposeServiceState decodes one Compose service-state JSON object.
func parseComposeServiceState(raw json.RawMessage, label string) (ComposeServiceState, error) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "{") {
		return ComposeServiceState{}, fmt.Errorf("parse docker compose ps JSON %s: expected object", label)
	}
	var state ComposeServiceState
	if err := json.Unmarshal(raw, &state); err != nil {
		return ComposeServiceState{}, fmt.Errorf("parse docker compose ps JSON %s: %w", label, err)
	}
	if firstNonEmpty(state.Service, state.Name) == "" {
		return ComposeServiceState{}, fmt.Errorf("parse docker compose ps JSON %s: missing service name", label)
	}
	return state, nil
}

// ParseDockerPSServiceRows decodes the fixed-field Docker label fallback.
func ParseDockerPSServiceRows(raw string) ([]ComposeServiceState, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []ComposeServiceState
	for index, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		state, err := parseDockerPSServiceRow(line, index+1)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}

// parseDockerPSServiceRow decodes one tab-separated Docker ps fallback line.
func parseDockerPSServiceRow(line string, index int) (ComposeServiceState, error) {
	fields := strings.SplitN(line, "\t", 6)
	if len(fields) != 6 {
		return ComposeServiceState{}, fmt.Errorf("parse docker ps service row %d: expected 6 tab-separated fields", index)
	}
	service := strings.TrimSpace(fields[0])
	name := strings.TrimSpace(fields[1])
	if service == "" && name == "" {
		return ComposeServiceState{}, fmt.Errorf("parse docker ps service row %d: missing service name", index)
	}
	status := strings.TrimSpace(fields[5])
	return ComposeServiceState{
		Service:  service,
		Name:     name,
		Image:    strings.TrimSpace(fields[2]),
		State:    strings.TrimSpace(fields[3]),
		Health:   normalizedDockerPSHealth(fields[4]),
		ExitCode: dockerPSExitCode(status),
	}, nil
}

// normalizedDockerPSHealth maps Docker's no-healthcheck marker to fibe-distilled's empty health.
func normalizedDockerPSHealth(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "none") {
		return ""
	}
	return value
}

// dockerPSExitCode extracts the numeric code from status strings like "Exited (255)".
func dockerPSExitCode(status string) int {
	status = strings.TrimSpace(status)
	start := strings.Index(status, "(")
	end := strings.Index(status, ")")
	if start < 0 || end <= start+1 {
		return 0
	}
	code, err := strconv.Atoi(strings.TrimSpace(status[start+1 : end]))
	if err != nil {
		return 0
	}
	return code
}

package runtime

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// Logs returns recent docker compose logs for a project or service.
func (c Checker) Logs(ctx context.Context, marquee domain.Marquee, project string, service string, tail int) ([]string, error) {
	if tail < 0 {
		return nil, errors.New("tail must be non-negative")
	}
	tailArg := strconv.Itoa(tail)
	if tail == 0 {
		tailArg = "all"
	}
	base, composeYAML, err := c.projectComposeYAML(ctx, marquee, project)
	if err != nil {
		return nil, err
	}
	lines, err := c.composeRuntime().Logs(ctx, marquee, project, base, composeYAML, service, tailArg)
	if err != nil {
		return nil, err
	}
	return lines, nil
}

// splitLogLines normalizes Compose log output into response lines.
func splitLogLines(logs string) []string {
	if logs == "" {
		return []string{}
	}
	return strings.Split(strings.TrimRight(logs, "\n"), "\n")
}

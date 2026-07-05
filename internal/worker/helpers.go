package worker

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// sourcePathsForPlayground returns remote Git checkout paths used by a Playground.
func sourcePathsForPlayground(pg domain.Playground, ps domain.Playspec) ([]string, error) {
	if pg.ComposeProject == nil || ps.BaseComposeYAML == "" {
		return nil, nil
	}
	summaries, err := validatedSourceServices(ps.BaseComposeYAML, "source paths")
	if err != nil {
		return nil, err
	}
	return sourcePathsForSummaries(*pg.ComposeProject, summaries), nil
}

// sourcePathsForSummaries returns checkout paths for repository-backed services.
func sourcePathsForSummaries(project string, summaries []service.Summary) []string {
	var paths []string
	for _, summary := range summaries {
		if path, ok := sourcePathForSummary(project, summary); ok {
			paths = append(paths, path)
		}
	}
	return paths
}

// sourcePathForSummary returns the remote checkout path for one service.
func sourcePathForSummary(project string, summary service.Summary) (string, bool) {
	if summary.RepoURL == "" {
		return "", false
	}
	return optfibe.SourceCheckoutPath(project, summary.RepoURL, service.SourceBranch(summary)), true
}

// validatedSourceServices validates Compose before source path or sync work.
func validatedSourceServices(composeYAML string, purpose string) ([]service.Summary, error) {
	validation := compose.Validate(composeYAML)
	if !validation.Valid {
		return nil, fmt.Errorf("validate compose for %s: %s", purpose, strings.Join(validation.Errors, "; "))
	}
	return validation.Services, nil
}

// idString formats a numeric store identifier for name-or-ID lookup helpers.
func idString(id int64) string {
	return strconv.FormatInt(id, 10)
}

// playgroundServiceNames returns nonblank service names for diagnostics.
func playgroundServiceNames(services []domain.PlaygroundServiceInfo) []string {
	out := make([]string, 0, len(services))
	for _, service := range services {
		if strings.TrimSpace(service.Name) != "" {
			out = append(out, service.Name)
		}
	}
	return out
}

// runtimeMarqueeForPlayground resolves the Marquee allowed for remote work.
func (w Worker) runtimeMarqueeForPlayground(ctx context.Context, pg domain.Playground) (domain.Marquee, error) {
	if pg.MarqueeID == nil {
		return domain.Marquee{}, store.ErrNotFound
	}
	mq, found, err := w.DB.GetRuntimeMarquee(ctx)
	if err != nil {
		return domain.Marquee{}, err
	}
	if !found {
		return domain.Marquee{}, store.ErrNotFound
	}
	return mq, nil
}

// refreshBlocked reports whether refresh cannot start or continue.
func refreshBlocked(active bool, err error) bool {
	return err != nil || !active
}

// refreshPhaseDone reports whether a refresh phase already has a final result.
func refreshPhaseDone(done bool, err error) bool {
	return done || err != nil
}

// firstNonEmpty returns the first nonblank string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// statusFromPlayground converts a Playground row using the current response time.
func statusFromPlayground(pg domain.Playground) domain.PlaygroundStatus {
	return pg.StatusSnapshot(time.Now().UTC())
}

// containsAny reports whether a string contains any configured fragment.
func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

// firstLine returns the first nonblank line of diagnostic output.
func firstLine(text string) string {
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// mergeErrorDetails copies existing details and sets one nested detail value.
func mergeErrorDetails(existing map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(existing)+1)
	maps.Copy(out, existing)
	out[key] = value
	return out
}

var (
	// gitCredentialURLPattern matches userinfo embedded in HTTP Git URLs.
	gitCredentialURLPattern = regexp.MustCompile(`(https?://)[^/@\s]*@`)
	// gitAuthHeaderPattern matches auth headers emitted by failed Git commands.
	gitAuthHeaderPattern = regexp.MustCompile(`(?i)(authorization:\s*(?:basic|bearer)\s+)[A-Za-z0-9._~+/=-]+`)
)

// redactGitCredentials removes token material from Git command output.
func redactGitCredentials(s string) string {
	s = gitCredentialURLPattern.ReplaceAllString(s, "${1}***@")
	return gitAuthHeaderPattern.ReplaceAllString(s, "${1}***")
}

// preserveString keeps current unless it is blank and latest is populated.
func preserveString(current string, latest string) string {
	if current != "" || latest == "" {
		return current
	}
	return latest
}

// preserveSlice keeps current unless it is empty and latest is populated.
func preserveSlice[T any](current []T, latest []T) []T {
	if len(current) > 0 || len(latest) == 0 {
		return current
	}
	return latest
}

// preservePointer keeps current unless it is nil and latest is populated.
func preservePointer[T any](current *T, latest *T) *T {
	if current != nil || latest == nil {
		return current
	}
	return latest
}

// newInternalPassword generates the per-Playground internal service password.
func newInternalPassword() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, 24)
	alphabetSize := big.NewInt(int64(len(alphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, alphabetSize)
		if err != nil {
			return "", fmt.Errorf("generate internal password: %w", err)
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}

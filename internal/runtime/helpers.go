package runtime

import (
	"context"
	"regexp"
	"strings"
	"time"
)

var (
	// imageComponentInvalid matches characters unsafe for image path components.
	imageComponentInvalid = regexp.MustCompile(`[^a-z0-9_.-]+`)
	// imageTagInvalid matches characters unsafe for image tags.
	imageTagInvalid = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	// gitCommitIDPattern matches one hex git object token.
	gitCommitIDPattern = regexp.MustCompile(`^[0-9A-Fa-f]{6,64}$`)
)

// ShellQuote returns a single-quoted POSIX shell literal.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// firstNonEmpty returns the first string that is not blank after trimming.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// pathHasParentSegment reports whether a slash path includes a parent segment.
func pathHasParentSegment(raw string) bool {
	for part := range strings.SplitSeq(raw, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// closeResource closes a runtime resource while intentionally ignoring errors.
func closeResource(resource interface{ Close() error }) {
	_ = resource.Close()
}

// withRuntimeTimeout adds a deadline only when the caller has not set one.
func withRuntimeTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// normalizedServiceHealth returns Compose health as a lowercase API value.
func normalizedServiceHealth(health string) string {
	return strings.ToLower(strings.TrimSpace(health))
}

// sanitizeImageComponent normalizes an image repository component.
func sanitizeImageComponent(raw string) string {
	out := imageComponentInvalid.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-")
	return strings.Trim(out, "-")
}

// sanitizeImageTag normalizes a Docker image tag.
func sanitizeImageTag(raw string) string {
	out := imageTagInvalid.ReplaceAllString(strings.TrimSpace(raw), "")
	return strings.Trim(out, ".-")
}

// validGitCommitID reports whether a commit string is one hex object token.
func validGitCommitID(raw string) bool {
	return gitCommitIDPattern.MatchString(raw)
}

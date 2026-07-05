package optfibe

import (
	"net/url"
	"regexp"
	"strings"
)

// defaultSourceBranch matches Fibe's default branch fallback for source paths.
const defaultSourceBranch = "main"

// unsafePathComponentChars matches characters not allowed in remote path names.
var unsafePathComponentChars = regexp.MustCompile(`[^a-z0-9_.]+`)

// SourceCheckoutParentPath returns the per-repository checkout directory.
func SourceCheckoutParentPath(project string, repoURL string) string {
	return PlaygroundPath(project) + "/props/" + pathSlugFromRepoURL(repoURL)
}

// SourceCheckoutPath returns the branch checkout path for a dynamic service.
func SourceCheckoutPath(project string, repoURL string, branch string) string {
	if branch == "" {
		branch = defaultSourceBranch
	}
	return SourceCheckoutParentPath(project, repoURL) + "/" + safePathComponent(branch)
}

// pathSlugFromRepoURL derives a stable checkout directory slug.
func pathSlugFromRepoURL(raw string) string {
	raw = repoSlugInput(raw)
	if raw == "" {
		return "source"
	}
	if slug, ok := urlRepoPathSlug(raw); ok {
		return slug
	}
	return pathPartsSlug(pathParts(strings.ReplaceAll(strings.TrimPrefix(raw, "git@"), ":", "/")), raw)
}

// repoSlugInput removes harmless repository URL spelling suffixes.
func repoSlugInput(raw string) string {
	return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(raw), "/"), ".git")
}

// urlRepoPathSlug derives a slug from URL-shaped repository inputs.
func urlRepoPathSlug(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	return pathPartsSlug(pathParts(parsed.Path), parsed.Host), true
}

// pathPartsSlug chooses owner-repo, repo, or fallback slug material.
func pathPartsSlug(parts []string, fallback string) string {
	if len(parts) >= 2 {
		return safePathComponent(parts[len(parts)-2] + "-" + parts[len(parts)-1])
	}
	if len(parts) == 1 {
		return safePathComponent(parts[0])
	}
	return safePathComponent(fallback)
}

// pathParts returns sanitized nonempty URL/path segments.
func pathParts(p string) []string {
	raw := strings.Split(strings.Trim(p, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(strings.TrimSuffix(part, ".git"))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// safePathComponent returns a safe remote path component.
func safePathComponent(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	// Trim dashes and dots so user-controlled values like ".." or "." cannot
	// survive as traversal-shaped path components.
	out := strings.Trim(unsafePathComponentChars.ReplaceAllString(raw, "-"), "-.")
	if out == "" {
		return "source"
	}
	return out
}

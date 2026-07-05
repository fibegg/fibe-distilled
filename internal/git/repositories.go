package git

import (
	"net/url"
	"regexp"
	"strings"
)

// githubRepositoryNamePattern mirrors SDK GitHub owner/repo validation.
var githubRepositoryNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// RepositoryHost reports whether a repository target is hosted on GitHub.
func RepositoryHost(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if strings.HasPrefix(raw, "git@") {
		userHost, _, ok := strings.Cut(raw, ":")
		if !ok {
			return false
		}
		_, host, ok := strings.Cut(userHost, "@")
		return ok && isGitHubHost(host)
	}
	if !strings.Contains(raw, "://") {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil && isGitHubHost(parsed.Hostname())
}

// RepositoryFullName extracts owner/repo from GitHub URL, SSH, or shorthand input.
func RepositoryFullName(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "git@") {
		return githubSSHFullName(raw)
	}
	if !strings.Contains(raw, "://") {
		return githubShortFullName(raw)
	}
	return githubURLFullName(raw)
}

// CloneableRepositoryURL reports whether a repository target can be passed to git clone.
func CloneableRepositoryURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	if RepositoryHost(raw) {
		_, ok := RepositoryFullName(raw)
		return ok
	}
	parsed, err := url.Parse(raw)
	if err == nil && cloneableRepositoryScheme(parsed.Scheme) && parsed.Host != "" && repositoryPathSegmentsSafe(parsed.Path) && !urlHasQueryOrFragment(parsed) {
		return true
	}
	return validSCPRepositoryURL(raw)
}

// SameRepositoryURL compares repository references by canonical identity.
func SameRepositoryURL(left string, right string) bool {
	if leftFullName, ok := RepositoryFullName(left); ok {
		rightFullName, rightOK := RepositoryFullName(right)
		return rightOK && strings.EqualFold(leftFullName, rightFullName)
	}
	return normalizeRepositoryURL(left) == normalizeRepositoryURL(right)
}

// githubURLFullName extracts owner/repo from HTTPS or SSH GitHub URLs.
func githubURLFullName(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || !RepositoryHost(raw) || !githubRepositoryURLScheme(parsed.Scheme) || urlHasQueryOrFragment(parsed) {
		return "", false
	}
	if !repositoryPathSegmentsSafe(parsed.Path) {
		return "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	return validatedGitHubFullName(parts[0], strings.TrimSuffix(parts[1], ".git"))
}

// githubRepositoryURLScheme reports URL schemes accepted for GitHub identity.
func githubRepositoryURLScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "https", "ssh":
		return true
	default:
		return false
	}
}

// urlHasQueryOrFragment reports URL suffixes that are not repository identity.
func urlHasQueryOrFragment(parsed *url.URL) bool {
	return parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != ""
}

// cloneableRepositoryScheme reports URL schemes fibe-distilled will pass to remote git.
func cloneableRepositoryScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "http", "https", "ssh", "git":
		return true
	default:
		return false
	}
}

// isGitHubHost reports GitHub's supported repository host names.
func isGitHubHost(host string) bool {
	return strings.EqualFold(host, "github.com") || strings.EqualFold(host, "www.github.com")
}

// githubSSHFullName extracts owner/repo from git@github.com:owner/repo.git.
func githubSSHFullName(raw string) (string, bool) {
	if !RepositoryHost(raw) {
		return "", false
	}
	_, repoPath, ok := strings.Cut(raw, ":")
	if !ok {
		return "", false
	}
	repoPath = strings.TrimSuffix(strings.TrimRight(repoPath, "/"), ".git")
	parts := strings.Split(repoPath, "/")
	if len(parts) != 2 {
		return "", false
	}
	return validatedGitHubFullName(parts[0], parts[1])
}

// githubShortFullName extracts owner/repo from SDK shorthand owner/repo[@ref].
func githubShortFullName(raw string) (string, bool) {
	repoSpec, _, _ := strings.Cut(raw, "@")
	repoSpec = strings.TrimRight(repoSpec, "/")
	parts := strings.Split(repoSpec, "/")
	if len(parts) != 2 {
		return "", false
	}
	return validatedGitHubFullName(parts[0], strings.TrimSuffix(parts[1], ".git"))
}

// validatedGitHubFullName returns owner/repo after applying SDK name rules.
func validatedGitHubFullName(owner string, repo string) (string, bool) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if !githubRepositoryNamePattern.MatchString(owner) || !githubRepositoryNamePattern.MatchString(repo) || repositorySegmentIsDot(owner) || repositorySegmentIsDot(repo) {
		return "", false
	}
	return owner + "/" + repo, true
}

// normalizeRepositoryURL removes harmless URL spelling differences.
func normalizeRepositoryURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	raw = strings.TrimSuffix(raw, ".git")
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		return parsed.String()
	}
	if normalized, ok := normalizeSCPRepositoryURL(raw); ok {
		return normalized
	}
	return raw
}

// normalizeSCPRepositoryURL normalizes host case in user@host:path references.
func normalizeSCPRepositoryURL(raw string) (string, bool) {
	userHost, repoPath, ok := strings.Cut(raw, ":")
	if !ok || !repositoryPathSegmentsSafe(repoPath) || strings.ContainsAny(userHost, "/ \t\r\n?#") || strings.ContainsAny(repoPath, " \t\r\n?#") {
		return "", false
	}
	user, host, ok := strings.Cut(userHost, "@")
	if !ok || user == "" || host == "" {
		return "", false
	}
	return user + "@" + strings.ToLower(host) + ":" + repoPath, true
}

// repositoryPathSegmentsSafe reports whether a repository path has concrete non-traversal segments.
func repositoryPathSegmentsSafe(repoPath string) bool {
	trimmed := strings.Trim(strings.TrimSpace(repoPath), "/")
	if trimmed == "" {
		return false
	}
	for segment := range strings.SplitSeq(trimmed, "/") {
		if !repositoryPathSegmentSafe(segment) {
			return false
		}
	}
	return true
}

// repositoryPathSegmentSafe rejects empty and traversal-shaped path segments.
func repositoryPathSegmentSafe(segment string) bool {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return false
	}
	if decoded, err := url.PathUnescape(segment); err == nil {
		segment = strings.TrimSpace(decoded)
	}
	if segment == "" || strings.ContainsAny(segment, `/\`) {
		return false
	}
	return !repositorySegmentIsDot(segment)
}

// repositorySegmentIsDot reports exact current or parent directory names.
func repositorySegmentIsDot(segment string) bool {
	return segment == "." || segment == ".."
}

// validSCPRepositoryURL accepts SSH shorthand targets such as git@example.com:owner/repo.git.
func validSCPRepositoryURL(raw string) bool {
	_, ok := normalizeSCPRepositoryURL(raw)
	return ok
}

package git

import (
	"net/url"
	"strings"
)

// RepositoryURLHasCredentials reports whether a repository URL embeds credentials.
func RepositoryURLHasCredentials(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User == nil {
		return false
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		return true
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "git+http", "git+https":
		return true
	default:
		return false
	}
}

// RedactRepositoryCredentials removes embedded credentials from display strings.
func RedactRepositoryCredentials(raw string) string {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.User == nil || !RepositoryURLHasCredentials(trimmed) {
		return raw
	}
	parsed.User = nil
	redacted := parsed.String()
	if strings.Contains(redacted, "://") {
		return strings.Replace(redacted, "://", "://***@", 1)
	}
	return "***@" + redacted
}

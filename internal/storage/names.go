package storage

import (
	"regexp"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/git"
)

// slugSeparators matches non-alphanumeric runs in generated names.
var slugSeparators = regexp.MustCompile(`[^a-z0-9]+`)

// inferPropName derives a stable Prop name from a repository URL.
func inferPropName(repo string) string {
	repo = strings.TrimSuffix(repo, ".git")
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return "prop"
	}
	return slug(parts[len(parts)-1])
}

// inferProvider classifies GitHub URLs while keeping generic Git support.
func inferProvider(repo string) string {
	if _, ok := git.RepositoryFullName(repo); ok {
		return "github"
	}
	return "git"
}

// slug normalizes names for generated resource defaults.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	out := strings.Trim(slugSeparators.ReplaceAllString(s, "-"), "-")
	if out == "" {
		return "fibe-distilled"
	}
	return out
}

package launch

import (
	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	"github.com/fibegg/fibe-distilled/internal/git"
)

// GitHubRepositoryURLs returns unique valid GitHub clone targets referenced by
// Fibe dynamic-build labels in a launch Compose document.
func GitHubRepositoryURLs(composeYAML string) []string {
	validation := compose.Validate(composeYAML)
	seen := map[string]bool{}
	var out []string
	for _, summary := range validation.Services {
		if _, ok := git.RepositoryFullName(summary.RepoURL); !ok || !git.CloneableRepositoryURL(summary.RepoURL) {
			continue
		}
		if seen[summary.RepoURL] {
			continue
		}
		seen[summary.RepoURL] = true
		out = append(out, summary.RepoURL)
	}
	return out
}

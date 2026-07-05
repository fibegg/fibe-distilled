package prop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/git"
	githubapi "github.com/fibegg/fibe-distilled/internal/github"
)

// repoStatus checks repository readiness for runtime source sync.
func (h Handler) repoStatus(w http.ResponseWriter, r *http.Request) {
	var body repoStatusPayload
	if !decode(w, r, &body) {
		return
	}
	if err := validateRepoStatusPayload(body); err != nil {
		writePayloadErr(w, r, err)
		return
	}
	repos := make([]map[string]any, 0, len(body.GitHubURLs))
	for _, raw := range body.GitHubURLs {
		repos = append(repos, h.repoStatusEntry(r.Context(), raw))
	}
	response.JSON(w, r, http.StatusOK, map[string]any{"repos": repos})
}

// validateRepoStatusPayload validates repository status request shape.
func validateRepoStatusPayload(body repoStatusPayload) error {
	if !body.fields.Has("github_urls") {
		return badRequestError{message: "github_urls is required"}
	}
	if len(body.GitHubURLs) == 0 {
		return badRequestError{message: "github_urls must be a non-empty array"}
	}
	for _, raw := range body.GitHubURLs {
		if strings.TrimSpace(raw) == "" {
			return badRequestError{message: "github_urls entries must not be blank"}
		}
	}
	return nil
}

// repoStatusEntry returns one SDK-compatible repository status entry.
func (h Handler) repoStatusEntry(ctx context.Context, raw string) map[string]any {
	safeURL := git.RedactRepositoryCredentials(raw)
	entry := map[string]any{"url": safeURL, "status": "ready", "authenticated": false}
	if git.RepositoryURLHasCredentials(raw) {
		entry["status"] = "invalid"
		entry["error"] = "repository URL must not include credentials; use process credentials or SSH access"
		return entry
	}
	if !validRepoStatusURL(raw) {
		entry["status"] = "invalid"
		entry["error"] = "invalid URL"
		return entry
	}
	fullName, github := git.RepositoryFullName(raw)
	if !github {
		return entry
	}
	entry["github_url"] = "https://github.com/" + fullName
	token := h.githubToken()
	entry["authenticated"] = token != ""
	writable := h.githubRepoWritable(ctx, raw, token)
	entry["runtime_writable"] = writable.writable
	entry["requires_fork"] = !writable.writable
	if writable.checked && writable.writable {
		entry["status"] = "ready"
		return entry
	}
	entry["status"] = "not_writable"
	if writable.reason == "" {
		writable.reason = "repository requires a writable GitHub token or fork"
	}
	entry["error"] = writable.reason
	return entry
}

// requireRuntimeWritable requires GitHub repos to be writable by the token.
func (h Handler) requireRuntimeWritable(ctx context.Context, repos []string) error {
	githubRepos, err := runtimeGitHubRepos(repos)
	if err != nil {
		return err
	}
	if len(githubRepos) == 0 {
		return nil
	}
	token := h.githubToken()
	for _, repo := range githubRepos {
		writable := h.githubRepoWritable(ctx, repo, token)
		if writable.checked && writable.writable {
			continue
		}
		return repositoryRequiresFork(repo, writable.reason)
	}
	return nil
}

// runtimeGitHubRepos filters runtime repositories to GitHub URLs.
func runtimeGitHubRepos(repos []string) ([]string, error) {
	var githubRepos []string
	for _, repo := range repos {
		if strings.TrimSpace(repo) == "" {
			continue
		}
		if err := validateRepositoryURLNoCredentials(repo); err != nil {
			return nil, apiValidationError{status: http.StatusBadRequest, code: "BAD_REQUEST", message: err.Error()}
		}
		if isGitHubURL(repo) {
			githubRepos = append(githubRepos, repo)
		}
	}
	return githubRepos, nil
}

// repositoryRequiresFork builds the writable-repository validation error.
func repositoryRequiresFork(repo string, reason string) error {
	if reason == "" {
		reason = "repository requires a writable GitHub token or fork"
	}
	return apiValidationError{
		status:  http.StatusUnprocessableEntity,
		code:    "REPOSITORY_REQUIRES_FORK",
		message: fmt.Sprintf("%s requires push/write access: %s", repo, reason),
	}
}

// writeRuntimeWritableErr maps repository writability errors.
func writeRuntimeWritableErr(w http.ResponseWriter, r *http.Request, err error) {
	if validation, ok := errors.AsType[apiValidationError](err); ok {
		response.Error(w, r, validation.status, validation.code, validation.message, validation.details)
		return
	}
	response.ServerError(w, r, err)
}

// repoWritableResult carries GitHub runtime writability evidence.
type repoWritableResult struct {
	writable bool
	checked  bool
	reason   string
}

// githubRepoWritable checks GitHub repository push-like permissions.
func (h Handler) githubRepoWritable(ctx context.Context, repoURL string, token string) repoWritableResult {
	if strings.TrimSpace(token) == "" {
		return repoWritableResult{checked: true, reason: "GitHub token is missing"}
	}
	fullName, ok := git.RepositoryFullName(repoURL)
	if !ok {
		return repoWritableResult{reason: "not a GitHub repository"}
	}
	client, err := githubapi.New(h.githubBaseURL(), token)
	if err != nil {
		return repoWritableResult{checked: true, reason: err.Error()}
	}
	repo, err := client.Repository(ctx, fullName)
	if err != nil {
		return repoWritableResult{checked: true, reason: err.Error()}
	}
	if githubRepoPushWritable(repo.Permissions) {
		return repoWritableResult{writable: true, checked: true, reason: "token has push permission"}
	}
	return repoWritableResult{checked: true, reason: "token can read " + fullName + " but cannot push"}
}

// githubRepoPushWritable reports whether permissions allow runtime source writes.
func githubRepoPushWritable(permissions map[string]bool) bool {
	for _, key := range githubWritablePermissionKeys {
		if permissions[key] {
			return true
		}
	}
	return false
}

// githubWritablePermissionKeys are GitHub permissions accepted for source writes.
var githubWritablePermissionKeys = []string{"push", "admin", "maintain"}

package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/git"
	"github.com/google/go-github/v75/github"
)

// PushEvent is the normalized subset of a GitHub push delivery.
type PushEvent struct {
	// RepositoryFullName is the owner/repository GitHub identity.
	RepositoryFullName string
	// Branch is the pushed branch name.
	Branch string
	// After is the pushed commit SHA after the update.
	After string
}

// webhookPayload is the subset of GitHub delivery JSON fibe-distilled consumes.
type webhookPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// ValidWebhookSignature verifies GitHub's sha256 HMAC signature.
func ValidWebhookSignature(secret string, signature string, body []byte) bool {
	secret = strings.TrimSpace(secret)
	signature = strings.TrimSpace(signature)
	if secret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

// PushEventFromWebhook converts a signed GitHub delivery body into worker input.
func PushEventFromWebhook(event string, body []byte) (PushEvent, bool, error) {
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return PushEvent{}, false, err
	}
	return pushEventFromPayload(event, payload), actionablePushPayload(event, payload), nil
}

// pushEventFromPayload normalizes a GitHub push payload after JSON parsing.
func pushEventFromPayload(event string, payload webhookPayload) PushEvent {
	if !actionablePushPayload(event, payload) {
		return PushEvent{}
	}
	branch, _ := strings.CutPrefix(strings.TrimSpace(payload.Ref), "refs/heads/")
	fullName, _ := git.RepositoryFullName(payload.Repository.FullName)
	return PushEvent{
		RepositoryFullName: fullName,
		Branch:             strings.TrimSpace(branch),
		After:              strings.TrimSpace(payload.After),
	}
}

// actionablePushPayload reports whether a GitHub delivery should trigger work.
func actionablePushPayload(event string, payload webhookPayload) bool {
	if strings.TrimSpace(event) != "push" {
		return false
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(payload.Ref), "refs/heads/")
	if !ok || strings.TrimSpace(branch) == "" || allZeroGitHubSHA(payload.After) {
		return false
	}
	_, ok = git.RepositoryFullName(payload.Repository.FullName)
	return ok
}

// allZeroGitHubSHA reports deleted-branch push markers.
func allZeroGitHubSHA(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	for _, r := range raw {
		if r != '0' {
			return false
		}
	}
	return true
}

// appendBranches converts GitHub branch payloads into fibe-distilled branch values.
func appendBranches(out []Branch, payload []*github.Branch) []Branch {
	for _, branch := range payload {
		if item, ok := branchFromPayload(branch); ok {
			out = append(out, item)
		}
	}
	return out
}

// branchFromPayload extracts the branch name and optional commit SHA.
func branchFromPayload(branch *github.Branch) (Branch, bool) {
	item := Branch{Name: branch.GetName()}
	if item.Name == "" {
		return Branch{}, false
	}
	if commit := branch.GetCommit(); commit != nil {
		item.SHA = commit.GetSHA()
	}
	return item, true
}

// repositoryNameParts carries a GitHub owner and repository name.
type repositoryNameParts struct {
	owner string
	repo  string
}

// splitFullName validates and splits an owner/repository string.
func splitFullName(fullName string) (repositoryNameParts, error) {
	owner, repo, ok := strings.Cut(strings.Trim(fullName, "/"), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return repositoryNameParts{}, errors.New("invalid GitHub repository name")
	}
	return repositoryNameParts{owner: owner, repo: repo}, nil
}

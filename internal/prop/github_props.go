package prop

import (
	"context"
	"errors"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/git"
	githubapi "github.com/fibegg/fibe-distilled/internal/github"
)

// syncGitHubPropMetadata refreshes GitHub repository metadata and branches.
func (h Handler) syncGitHubPropMetadata(ctx context.Context, p domain.Prop) (domain.Prop, error) {
	now := time.Now().UTC()
	fullName, ok := git.RepositoryFullName(p.RepositoryURL)
	if !ok {
		return p, errors.New("not a GitHub repository")
	}
	client, err := githubapi.New(h.githubBaseURL(), h.githubToken())
	if err != nil {
		return p, err
	}
	repo, err := client.Repository(ctx, fullName)
	if err != nil {
		return p, err
	}
	applyGitHubRepositoryMetadata(&p, repo)

	payload, err := client.Branches(ctx, fullName)
	if err != nil {
		return p, err
	}
	p.Branches, p.BranchRecords = normalizeBranchRecords(p.DefaultBranch, githubPropBranches(payload), now)
	p.Status = "active"
	p.LastSyncedAt = &now
	return p, nil
}

// applyGitHubRepositoryMetadata updates Prop fields fetched from GitHub.
func applyGitHubRepositoryMetadata(p *domain.Prop, repo githubapi.Repository) {
	if repo.DefaultBranch != "" {
		p.DefaultBranch = repo.DefaultBranch
	} else if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	p.Provider = "github"
	p.Private = repo.Private
}

// githubPropBranches converts GitHub branch payloads to domain branches.
func githubPropBranches(payload []githubapi.Branch) []domain.PropBranch {
	branches := make([]domain.PropBranch, len(payload))
	for i, branch := range payload {
		branches[i] = domain.PropBranch{Name: branch.Name, HeadSHA: branch.SHA}
	}
	return branches
}

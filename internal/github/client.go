package github

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v75/github"
)

// defaultHTTPTimeout bounds GitHub API calls that do not receive a deadline.
const defaultHTTPTimeout = 30 * time.Second

// Client is a small wrapper around go-github for fibe-distilled repository operations.
type Client struct {
	client *github.Client
}

// Repository contains the GitHub repository metadata fibe-distilled exposes.
type Repository struct {
	// DefaultBranch is the repository's GitHub default branch.
	DefaultBranch string
	// Private reports whether GitHub marks the repository private.
	Private bool
	// Permissions contains GitHub's permission flags for the token.
	Permissions map[string]bool
}

// Branch contains a GitHub branch name and head SHA.
type Branch struct {
	// Name is the branch name.
	Name string
	// SHA is the branch head commit SHA.
	SHA string
}

// New constructs a GitHub client using an optional API base URL and token.
func New(baseURL string, token string) (*Client, error) {
	client := github.NewClient(&http.Client{Timeout: defaultHTTPTimeout})
	token = strings.TrimSpace(token)
	if token != "" {
		client = client.WithAuthToken(token)
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" && baseURL != "https://api.github.com" {
		parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
		if err != nil {
			return nil, err
		}
		client.BaseURL = parsed
	}
	return &Client{client: client}, nil
}

// Repository fetches metadata for an owner/repository name.
func (c *Client) Repository(ctx context.Context, fullName string) (Repository, error) {
	parts, err := splitFullName(fullName)
	if err != nil {
		return Repository{}, err
	}
	payload, _, err := c.client.Repositories.Get(ctx, parts.owner, parts.repo)
	if err != nil {
		return Repository{}, err
	}
	return Repository{
		DefaultBranch: payload.GetDefaultBranch(),
		Private:       payload.GetPrivate(),
		Permissions:   payload.Permissions,
	}, nil
}

// Branches lists repository branches using GitHub pagination.
func (c *Client) Branches(ctx context.Context, fullName string) ([]Branch, error) {
	parts, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}
	opts := &github.BranchListOptions{ListOptions: github.ListOptions{PerPage: 100}}
	var branches []Branch
	for {
		payload, resp, err := c.client.Repositories.ListBranches(ctx, parts.owner, parts.repo, opts)
		if err != nil {
			return nil, err
		}
		branches = appendBranches(branches, payload)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return branches, nil
}

package prop

import (
	"context"
	"net/http"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// Repository is the Prop storage surface used by the HTTP handler.
type Repository interface {
	// ListProps returns all persisted Props.
	ListProps(ctx context.Context) ([]domain.Prop, error)
	// CreateProp persists a new Prop.
	CreateProp(ctx context.Context, p domain.Prop) (domain.Prop, error)
	// GetProp loads a Prop by ID or name.
	GetProp(ctx context.Context, identifier string) (domain.Prop, error)
	// SaveProp persists Prop changes.
	SaveProp(ctx context.Context, p domain.Prop) (domain.Prop, error)
	// DeleteProp removes a Prop.
	DeleteProp(ctx context.Context, identifier string) error
	// PlayspecsReferencingProp returns Playspec names that still reference the Prop.
	PlayspecsReferencingProp(ctx context.Context, p domain.Prop) ([]string, error)
}

// Options configures integrations needed by Prop handlers.
type Options struct {
	// GitHubBaseURL is the GitHub API endpoint used by repository checks.
	GitHubBaseURL string
	// GitHubToken is the process GitHub token used by repository checks.
	GitHubToken string
}

// Handler owns Prop HTTP behavior and repository metadata endpoints.
type Handler struct {
	repo             Repository
	githubBaseURLVal string
	githubTokenVal   string
}

// NewHandler constructs a Prop handler.
func NewHandler(repo Repository, options Options) Handler {
	return Handler{
		repo:             repo,
		githubBaseURLVal: normalizeGitHubBaseURL(options.GitHubBaseURL),
		githubTokenVal:   strings.TrimSpace(options.GitHubToken),
	}
}

// normalizeGitHubBaseURL normalizes an optional GitHub API endpoint.
func normalizeGitHubBaseURL(configured string) string {
	value := strings.TrimSpace(configured)
	if value == "" {
		return "https://api.github.com"
	}
	return value
}

// githubBaseURL returns the active GitHub API endpoint.
func (h Handler) githubBaseURL() string {
	return h.githubBaseURLVal
}

// githubToken returns the active GitHub token.
func (h Handler) githubToken() string {
	return h.githubTokenVal
}

// Compile-time guard for the SQLite repository.
var _ Repository = (*store.DB)(nil)

// List serves Prop list requests.
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	h.propsList(w, r)
}

// Create serves Prop create requests.
func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	h.propsCreate(w, r)
}

// Get serves Prop lookup requests.
func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	h.propsGet(w, r)
}

// Update serves Prop update requests.
func (h Handler) Update(w http.ResponseWriter, r *http.Request) {
	h.propsUpdate(w, r)
}

// Delete serves Prop delete requests.
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	h.propsDelete(w, r)
}

// Branches serves Prop branch metadata requests.
func (h Handler) Branches(w http.ResponseWriter, r *http.Request) {
	h.propsBranches(w, r)
}

// Sync serves Prop repository-sync requests.
func (h Handler) Sync(w http.ResponseWriter, r *http.Request) {
	h.propsSync(w, r)
}

// RepoStatus serves repository readiness checks.
func (h Handler) RepoStatus(w http.ResponseWriter, r *http.Request) {
	h.repoStatus(w, r)
}

// RequireRuntimeWritable verifies that runtime GitHub sources can be pushed by the configured token.
func (h Handler) RequireRuntimeWritable(ctx context.Context, repos []string) error {
	return h.requireRuntimeWritable(ctx, repos)
}

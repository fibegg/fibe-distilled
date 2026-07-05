package playground

import (
	"context"
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// Repository persists Playground rows and loads their dependencies.
type Repository interface {
	// ListPlaygrounds returns all persisted Playgrounds.
	ListPlaygrounds(context.Context) ([]domain.Playground, error)
	// GetPlayground loads a Playground by ID or name.
	GetPlayground(context.Context, string) (domain.Playground, error)
	// CreatePlayground persists a new Playground row.
	CreatePlayground(context.Context, domain.Playground) (domain.Playground, error)
	// SavePlayground persists Playground state changes.
	SavePlayground(context.Context, domain.Playground) (domain.Playground, error)
	// DeletePlayground removes a Playground row.
	DeletePlayground(context.Context, string) error
	// GetPlayspec loads a Playground Playspec dependency.
	GetPlayspec(context.Context, string) (domain.Playspec, error)
	// GetMarquee loads a Playground Marquee dependency.
	GetMarquee(context.Context, string) (domain.Marquee, error)
	// GetRuntimeMarquee loads the configured runtime Marquee.
	GetRuntimeMarquee(context.Context) (domain.Marquee, bool, error)
}

// AsyncSupervisor persists and executes Playground async responses.
type AsyncSupervisor interface {
	// Enqueue persists and starts an async operation.
	Enqueue(context.Context, func(context.Context) (map[string]any, *domain.APIError)) (domain.AsyncOperation, error)
}

// Runtime controls local Compose actions for existing Playground runtimes.
type Runtime interface {
	// DestroyCompose removes a deployed Compose project.
	DestroyCompose(context.Context, domain.Marquee, string) error
	// StartCompose starts an existing Compose project.
	StartCompose(context.Context, domain.Marquee, string) error
	// DownCompose stops and removes an existing Compose project.
	DownCompose(context.Context, domain.Marquee, string) error
	// StopCompose stops an existing Compose project.
	StopCompose(context.Context, domain.Marquee, string) error
	// Logs returns Compose logs for one service or project.
	Logs(context.Context, domain.Marquee, string, string, int) ([]string, error)
}

// Services provides deployment and observation behavior owned outside HTTP routing.
type Services interface {
	AsyncSupervisor
	// DeployPlayground renders and deploys a Playground.
	DeployPlayground(context.Context, domain.Playground, domain.Playspec, *domain.Marquee) (domain.Playground, error)
	// RefreshPlayground observes and reconciles runtime status.
	RefreshPlayground(context.Context, domain.Playground) (domain.PlaygroundStatus, error)
}

// Options wires Playground to server-scoped dependencies.
type Options struct {
	// ResolveConfiguredMarqueeID resolves name-or-ID Marquee inputs in single-Marquee scope.
	ResolveConfiguredMarqueeID func(context.Context, *int64, string) (*int64, error)
}

// Handler owns Playground HTTP route entrypoints and resource orchestration.
type Handler struct {
	repo                       Repository
	services                   Services
	runtime                    Runtime
	resolveConfiguredMarqueeID func(context.Context, *int64, string) (*int64, error)
}

// NewHandler constructs a Playground handler.
func NewHandler(repo Repository, services Services, runtime Runtime, options Options) Handler {
	resolver := options.ResolveConfiguredMarqueeID
	if resolver == nil {
		resolver = func(_ context.Context, id *int64, _ string) (*int64, error) {
			return id, nil
		}
	}
	return Handler{
		repo:                       repo,
		services:                   services,
		runtime:                    runtime,
		resolveConfiguredMarqueeID: resolver,
	}
}

// List returns filtered Playground resources.
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	h.playgroundsList(w, r)
}

// Create creates and deploys a Playground synchronously.
func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	h.playgroundsCreate(w, r)
}

// Get returns one Playground by name or ID.
func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	h.playgroundsGet(w, r)
}

// Update applies metadata or runtime config changes.
func (h Handler) Update(w http.ResponseWriter, r *http.Request) {
	h.playgroundsUpdate(w, r)
}

// Delete destroys runtime state then deletes the row.
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	h.playgroundsDelete(w, r)
}

// Status returns the current Playground lifecycle state.
func (h Handler) Status(w http.ResponseWriter, r *http.Request) {
	h.playgroundsStatus(w, r)
}

// Refresh observes remote runtime state and returns Playground status.
func (h Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	h.playgroundsStatusRefresh(w, r)
}

// Logs queues live or cached Playground log collection.
func (h Handler) Logs(w http.ResponseWriter, r *http.Request) {
	h.playgroundsLogs(w, r)
}

// Operations handles rollout, restart, start, stop, and retry actions.
func (h Handler) Operations(w http.ResponseWriter, r *http.Request) {
	h.playgroundsOperations(w, r)
}

// Expiration extends an active Playground lifetime.
func (h Handler) Expiration(w http.ResponseWriter, r *http.Request) {
	h.playgroundsExpiration(w, r)
}

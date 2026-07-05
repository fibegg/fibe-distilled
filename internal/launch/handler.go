package launch

import (
	"context"
	"errors"
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/domain"
	playgroundpkg "github.com/fibegg/fibe-distilled/internal/playground"
)

// Repository persists Launch-created resources and loads Marquees.
type Repository interface {
	// CreatePlayspec stores the Launch-generated Playspec.
	CreatePlayspec(context.Context, domain.Playspec) (domain.Playspec, error)
	// GetPlayspec loads a Playspec by ID or name.
	GetPlayspec(context.Context, string) (domain.Playspec, error)
	// DeletePlayspec removes a Launch-created Playspec during rollback.
	DeletePlayspec(context.Context, string) error
	// CreateProp stores the Launch-generated Prop.
	CreateProp(context.Context, domain.Prop) (domain.Prop, error)
	// GetProp loads a Prop by ID or name.
	GetProp(context.Context, string) (domain.Prop, error)
	// DeleteProp removes a Launch-created Prop during rollback.
	DeleteProp(context.Context, string) error
	// GetMarquee loads the resolved runtime Marquee.
	GetMarquee(context.Context, string) (domain.Marquee, error)
}

// RepositoryPreflight verifies repositories before Launch creates runtime state.
type RepositoryPreflight interface {
	// RequireRuntimeWritable verifies source repositories are writable before Launch creates runtime state.
	RequireRuntimeWritable(context.Context, []string) error
}

// PlaygroundCreator creates and deploys runtime Playgrounds for Launch.
type PlaygroundCreator interface {
	// CreateAndDeploy creates and deploys a Playground synchronously.
	CreateAndDeploy(context.Context, playgroundpkg.CreatePayload) (domain.Playground, error)
	// CreateAndDeployDetached creates a Playground and deploys it in background.
	CreateAndDeployDetached(context.Context, playgroundpkg.CreatePayload) (domain.Playground, error)
}

// Options wires Launch to neighboring resource services.
type Options struct {
	// ResolveConfiguredMarqueeID resolves name-or-ID Marquee inputs in single-Marquee scope.
	ResolveConfiguredMarqueeID func(context.Context, *int64, string) (*int64, error)
	// Repositories verifies source repositories before runtime state is created.
	Repositories RepositoryPreflight
	// Playgrounds creates and deploys runtime Playgrounds.
	Playgrounds PlaygroundCreator
}

// Handler owns Launch HTTP route entrypoints and orchestration.
type Handler struct {
	repo                       Repository
	resolveConfiguredMarqueeID func(context.Context, *int64, string) (*int64, error)
	repositories               RepositoryPreflight
	playgrounds                PlaygroundCreator
}

// NewHandler constructs a Launch handler.
func NewHandler(repo Repository, options Options) Handler {
	return Handler{
		repo:                       repo,
		resolveConfiguredMarqueeID: defaultResolveConfiguredMarqueeID(options.ResolveConfiguredMarqueeID),
		repositories:               options.Repositories,
		playgrounds:                options.Playgrounds,
	}
}

// Create serves launch create requests.
func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeLaunchPayload(w, r)
	if !ok {
		return
	}
	plan, ok := h.prepareLaunchPlan(w, r, body)
	if !ok {
		return
	}
	resources, ok := h.createLaunchResources(w, r, plan)
	if !ok {
		return
	}
	if plan.CreatePlayground && !h.createLaunchPlayground(w, r, plan, &resources) {
		return
	}
	writeLaunchCreated(w, r, plan, resources)
}

// defaultResolveConfiguredMarqueeID keeps tests usable when no Marquee resolver is configured.
func defaultResolveConfiguredMarqueeID(fn func(context.Context, *int64, string) (*int64, error)) func(context.Context, *int64, string) (*int64, error) {
	if fn != nil {
		return fn
	}
	return func(_ context.Context, id *int64, _ string) (*int64, error) { return id, nil }
}

// requireRuntimeWritable keeps repository-free launches independent of GitHub.
func (h Handler) requireRuntimeWritable(ctx context.Context, repos []string) error {
	if h.repositories == nil {
		return nil
	}
	return endpointError(h.repositories.RequireRuntimeWritable(ctx, repos))
}

// deployRuntimePlayground delegates creation/deploy to the Playground service.
func (h Handler) deployRuntimePlayground(ctx context.Context, payload playgroundpkg.CreatePayload, detached bool) (domain.Playground, error) {
	if h.playgrounds == nil {
		return domain.Playground{}, errors.New("launch playground deployer is not configured")
	}
	if detached {
		pg, err := h.playgrounds.CreateAndDeployDetached(ctx, payload)
		return pg, endpointError(err)
	}
	pg, err := h.playgrounds.CreateAndDeploy(ctx, payload)
	return pg, endpointError(err)
}

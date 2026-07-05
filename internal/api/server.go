package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/async"
	"github.com/fibegg/fibe-distilled/internal/compatgate"
	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/domain"
	launchpkg "github.com/fibegg/fibe-distilled/internal/launch"
	marqueepkg "github.com/fibegg/fibe-distilled/internal/marquee"
	playgroundpkg "github.com/fibegg/fibe-distilled/internal/playground"
	playspecpkg "github.com/fibegg/fibe-distilled/internal/playspec"
	proppkg "github.com/fibegg/fibe-distilled/internal/prop"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"github.com/fibegg/fibe-distilled/internal/worker"
	"github.com/go-chi/chi/v5"
)

// Server is the fibe-distilled HTTP server and request boundary.
type Server struct {
	cfg        config.Config
	store      *store.DB
	worker     worker.Worker
	mux        chi.Router
	gate       *compatgate.Gate
	async      async.Handler
	marquee    marqueepkg.Handler
	playspec   playspecpkg.Handler
	prop       proppkg.Handler
	launch     launchpkg.Handler
	playground playgroundpkg.Handler
}

// Options configures server construction seams.
type Options struct {
	// GitHubBaseURL overrides the GitHub API endpoint used by repository checks.
	GitHubBaseURL string
}

// New constructs a Server with routes, auth, and compatibility gate wired.
func New(cfg config.Config, st *store.DB, wk worker.Worker) *Server {
	return NewWithOptions(cfg, st, wk, Options{})
}

// NewWithOptions constructs a Server with explicit construction-time options.
func NewWithOptions(cfg config.Config, st *store.DB, wk worker.Worker, options Options) *Server {
	s := &Server{
		cfg:      cfg,
		store:    st,
		worker:   wk,
		mux:      chi.NewRouter(),
		gate:     compatgate.New(),
		async:    async.NewHandler(st),
		marquee:  marqueepkg.NewHandler(st),
		playspec: playspecpkg.NewHandler(st),
	}
	s.prop = proppkg.NewHandler(st, proppkg.Options{
		GitHubBaseURL: options.GitHubBaseURL,
		GitHubToken:   cfg.GitHubTok,
	})
	s.playground = playgroundpkg.NewHandler(st, wk, wk.Runtime, playgroundpkg.Options{
		ResolveConfiguredMarqueeID: s.marquee.ResolveConfiguredID,
	})
	s.launch = launchpkg.NewHandler(st, launchpkg.Options{
		ResolveConfiguredMarqueeID: s.marquee.ResolveConfiguredID,
		Repositories:               s.prop,
		Playgrounds:                s.playground,
	})
	s.routes()
	return s
}

// ServeHTTP applies request IDs, auth, compatibility gating, and routing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = response.WithRequestID(r, domain.RandomID("req_", 8))
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !s.authorized(r) {
			response.Unauthorized(w, r)
			return
		}
		if decision := s.gate.Check(r); !decision.Allowed {
			response.Error(w, r, decision.Status, decision.Code, decision.Message, decision.Details)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// up handles the public health check endpoint.
func (s *Server) up(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, r, http.StatusOK, map[string]any{"status": "ok"})
}

// me returns the fixed single-owner identity.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	email := "owner@fibe-distilled.local"
	response.JSON(w, r, http.StatusOK, map[string]any{
		"id":             1,
		"username":       "fibe-distilled-owner",
		"email":          email,
		"name":           "fibe-distilled owner",
		"role":           "owner",
		"api_key_scopes": []string{"*"},
	})
}

// status returns a compact server/resource summary.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	resources, err := s.statusResources(r.Context())
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	counts := playgroundStatusCounts(resources.playgrounds)
	response.JSON(w, r, http.StatusOK, statusPayload(resources, counts))
}

// statusResources contains the resources summarized by /api/status.
type statusResources struct {
	playgrounds []domain.Playground
	props       []domain.Prop
	playspecs   []domain.Playspec
	marquees    []domain.Marquee
}

// statusResources loads all resources needed for the status endpoint.
func (s *Server) statusResources(ctx context.Context) (statusResources, error) {
	playgrounds, err := s.store.ListPlaygrounds(ctx)
	if err != nil {
		return statusResources{}, err
	}
	props, err := s.store.ListProps(ctx)
	if err != nil {
		return statusResources{}, err
	}
	playspecs, err := s.store.ListPlayspecs(ctx)
	if err != nil {
		return statusResources{}, err
	}
	marquees, err := s.configuredMarqueeList(ctx)
	if err != nil {
		return statusResources{}, err
	}
	return statusResources{playgrounds: playgrounds, props: props, playspecs: playspecs, marquees: marquees}, nil
}

// playgroundStatusSummary is the /api/status Playground count block.
type playgroundStatusSummary struct {
	total   int
	active  int
	stopped int
}

// playgroundStatusCounts groups Playground lifecycle states for /api/status.
func playgroundStatusCounts(playgrounds []domain.Playground) playgroundStatusSummary {
	counts := playgroundStatusSummary{total: len(playgrounds)}
	for _, pg := range playgrounds {
		switch pg.Status {
		case domain.StatusRunning, domain.StatusInProgress, domain.StatusPending, domain.StatusHasChanges:
			counts.active++
		case domain.StatusStopped:
			counts.stopped++
		}
	}
	return counts
}

// statusPayload renders the Fibe-shaped server status response.
func statusPayload(resources statusResources, counts playgroundStatusSummary) map[string]any {
	return map[string]any{
		"status":      "ok",
		"server":      "fibe-distilled",
		"version":     "0.1.0",
		"playgrounds": map[string]any{"total": counts.total, "active": counts.active, "stopped": counts.stopped},
		"props":       len(resources.props),
		"playspecs":   len(resources.playspecs),
		"marquees":    len(resources.marquees),
		"secrets":     0,
		"api_keys":    0,
		"subscription": map[string]any{
			"plan":             "fibe-distilled",
			"playground_limit": 0,
		},
	}
}

// serverInfo returns the instance identity and feature flags.
func (s *Server) serverInfo(w http.ResponseWriter, r *http.Request) {
	serverID, err := s.store.ServerID(r.Context())
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusOK, map[string]any{
		"name":      "fibe-distilled",
		"version":   "0.1.0",
		"server_id": serverID,
		"features": map[string]bool{
			"playgrounds":          true,
			"github_push_webhooks": strings.TrimSpace(s.cfg.GitHubWebhookSecret) != "",
		},
	})
}

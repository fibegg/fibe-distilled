package api

import (
	"net/http"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/response"
)

// routes registers the supported fibe-distilled HTTP surface.
func (s *Server) routes() {
	s.mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		response.NotFound(w, r, "route")
	})
	s.mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed for route", map[string]any{"method": r.Method})
	})

	s.mux.Get("/up.json", s.up)
	s.mux.Post("/webhooks/github", s.githubWebhook)
	s.mux.Get("/api/me", s.me)
	s.mux.Get("/api/status", s.status)
	s.mux.Get("/api/server-info", s.serverInfo)
	s.mux.Get("/api/async_requests/{id}", s.async.Show)

	s.mux.Get("/api/marquees", s.marquee.List)
	s.mux.Get("/api/marquees/{identifier}", s.marquee.Get)

	s.mux.Get("/api/props", s.prop.List)
	s.mux.Post("/api/props", s.prop.Create)
	s.mux.Get("/api/props/{identifier}", s.prop.Get)
	s.mux.Patch("/api/props/{identifier}", s.prop.Update)
	s.mux.Delete("/api/props/{identifier}", s.prop.Delete)
	s.mux.Get("/api/props/{identifier}/branches", s.prop.Branches)
	s.mux.Post("/api/props/{identifier}/syncs", s.prop.Sync)
	s.mux.Post("/api/repo_status_checks", s.prop.RepoStatus)

	s.mux.Get("/api/playspecs", s.playspec.List)
	s.mux.Post("/api/playspecs", s.playspec.Create)
	s.mux.Get("/api/playspecs/{identifier}", s.playspec.Get)
	s.mux.Patch("/api/playspecs/{identifier}", s.playspec.Update)
	s.mux.Delete("/api/playspecs/{identifier}", s.playspec.Delete)
	s.mux.Get("/api/playspecs/{identifier}/services", s.playspec.Services)

	s.mux.Post("/api/launches", s.launch.Create)

	s.mux.Get("/api/playgrounds", s.playground.List)
	s.mux.Post("/api/playgrounds", s.playground.Create)
	s.mux.Get("/api/playgrounds/{identifier}", s.playground.Get)
	s.mux.Patch("/api/playgrounds/{identifier}", s.playground.Update)
	s.mux.Delete("/api/playgrounds/{identifier}", s.playground.Delete)
	s.mux.Get("/api/playgrounds/{identifier}/status", s.playground.Status)
	s.mux.Post("/api/playgrounds/{identifier}/status", s.playground.Refresh)
	s.mux.Post("/api/playgrounds/{identifier}/logs", s.playground.Logs)
	s.mux.Post("/api/playgrounds/{identifier}/operations", s.playground.Operations)
	s.mux.Post("/api/playgrounds/{identifier}/expiration", s.playground.Expiration)

	s.mux.HandleFunc("/api/*", s.apiNotImplemented)
}

// apiNotImplemented is the defensive catch-all after compatgate classification.
func (s *Server) apiNotImplemented(w http.ResponseWriter, r *http.Request) {
	response.NotImplemented(w, r, strings.TrimPrefix(r.URL.Path, "/api/"))
}

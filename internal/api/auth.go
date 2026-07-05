package api

import (
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/api/auth"
)

// authorized checks the static bearer token for API requests.
func (s *Server) authorized(r *http.Request) bool {
	return auth.Authorized(r.Header.Get("Authorization"), s.cfg.APIToken)
}

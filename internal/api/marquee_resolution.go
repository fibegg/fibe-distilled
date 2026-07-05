package api

import (
	"context"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// configuredMarqueeList returns the only Marquee visible to clients.
func (s *Server) configuredMarqueeList(ctx context.Context) ([]domain.Marquee, error) {
	return s.marquee.ConfiguredList(ctx)
}

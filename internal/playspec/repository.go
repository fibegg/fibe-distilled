package playspec

import (
	"context"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// Repository stores and loads Playspec resources.
type Repository interface {
	// ListPlayspecs loads all Playspec rows.
	ListPlayspecs(context.Context) ([]domain.Playspec, error)
	// CreatePlayspec inserts one Playspec row.
	CreatePlayspec(context.Context, domain.Playspec) (domain.Playspec, error)
	// GetPlayspec loads one Playspec by name or ID.
	GetPlayspec(context.Context, string) (domain.Playspec, error)
	// SavePlayspec persists one Playspec row.
	SavePlayspec(context.Context, domain.Playspec) (domain.Playspec, error)
	// DeletePlayspec removes one Playspec by name or ID.
	DeletePlayspec(context.Context, string) error
	// CountPlaygroundsForPlayspec counts Playground references for delete safety.
	CountPlaygroundsForPlayspec(context.Context, int64) (int, error)
}

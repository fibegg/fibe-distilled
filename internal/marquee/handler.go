package marquee

import (
	"context"
	"errors"
	"net/http"
	"time"

	apilist "github.com/fibegg/fibe-distilled/internal/api/list"
	"github.com/fibegg/fibe-distilled/internal/api/request"
	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// Repository loads Marquee rows from the persistence substrate.
type Repository interface {
	// GetRuntimeMarquee loads the startup-configured runtime Marquee.
	GetRuntimeMarquee(context.Context) (domain.Marquee, bool, error)
	// GetMarquee loads one Marquee by name or ID.
	GetMarquee(context.Context, string) (domain.Marquee, error)
}

// Handler owns Marquee API handlers and single-Marquee visibility policy.
type Handler struct {
	repo Repository
}

// NewHandler constructs a Marquee handler.
func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

// List returns the read-only startup-configured Marquee.
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.ConfiguredList(r.Context())
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	filtered, err := filterMarquees(r, items)
	if err != nil {
		response.BadRequest(w, r, err.Error())
		return
	}
	response.List(w, r, filtered)
}

// Get returns one visible Marquee by name or ID.
func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	m, err := h.repo.GetMarquee(r.Context(), request.PathValue(r, "identifier"))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	if visible, err := h.Visible(r.Context(), m); err != nil {
		response.ServerError(w, r, err)
		return
	} else if !visible {
		response.NotFound(w, r, "marquee")
		return
	}
	response.JSON(w, r, http.StatusOK, m)
}

// Configured returns the startup-configured Marquee row if present.
func (h Handler) Configured(ctx context.Context) (domain.Marquee, bool, error) {
	return h.repo.GetRuntimeMarquee(ctx)
}

// ConfiguredList returns the only Marquee visible to clients.
func (h Handler) ConfiguredList(ctx context.Context) ([]domain.Marquee, error) {
	configured, configuredOK, err := h.Configured(ctx)
	if err != nil {
		return nil, err
	}
	if !configuredOK {
		return []domain.Marquee{}, nil
	}
	return []domain.Marquee{configured}, nil
}

// Visible reports whether a Marquee matches the configured Marquee.
func (h Handler) Visible(ctx context.Context, candidate domain.Marquee) (bool, error) {
	configured, configuredOK, err := h.Configured(ctx)
	if err != nil {
		return false, err
	}
	return sameConfigured(configured, configuredOK, candidate), nil
}

// ResolveConfiguredID resolves launch/create Marquee references.
func (h Handler) ResolveConfiguredID(ctx context.Context, marqueeID *int64, identifier string) (*int64, error) {
	configured, configuredOK, err := h.Configured(ctx)
	if err != nil {
		return nil, err
	}
	if marqueeID != nil {
		return explicitConfiguredID(marqueeID, configured, configuredOK)
	}
	if identifier != "" {
		return h.namedConfiguredID(ctx, identifier, configured, configuredOK)
	}
	return defaultConfiguredID(configured, configuredOK), nil
}

// sameConfigured checks whether a Marquee is visible in single-Marquee mode.
func sameConfigured(configured domain.Marquee, configuredOK bool, candidate domain.Marquee) bool {
	return configuredOK && configured.ID == candidate.ID
}

// explicitConfiguredID aliases positive numeric IDs to configured Marquee.
func explicitConfiguredID(marqueeID *int64, configured domain.Marquee, configuredOK bool) (*int64, error) {
	if configuredOK {
		if *marqueeID <= 0 {
			return nil, store.ErrNotFound
		}
		id := configured.ID
		return &id, nil
	}
	return marqueeID, nil
}

// namedConfiguredID resolves named Marquee references within scope.
func (h Handler) namedConfiguredID(ctx context.Context, identifier string, configured domain.Marquee, configuredOK bool) (*int64, error) {
	mq, err := h.repo.GetMarquee(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if configuredOK && mq.ID != configured.ID {
		return nil, store.ErrNotFound
	}
	id := mq.ID
	return &id, nil
}

// defaultConfiguredID supplies the configured Marquee when omitted.
func defaultConfiguredID(configured domain.Marquee, configuredOK bool) *int64 {
	if !configuredOK {
		return nil
	}
	id := configured.ID
	return &id
}

// filterMarquees applies supported Marquee list filters.
func filterMarquees(r *http.Request, items []domain.Marquee) ([]domain.Marquee, error) {
	q := r.URL.Query()
	status, err := apilist.QueryExact(q, "status")
	if err != nil {
		return nil, err
	}
	filtered := apilist.SelectItems(items, func(item domain.Marquee) bool {
		if status.Present && item.Status != status.Value {
			return false
		}
		return apilist.MatchesText(item.Name, q.Get("q")) &&
			apilist.MatchesText(item.Name, q.Get("name"))
	})
	return apilist.ApplyNamedCommon(
		r,
		filtered,
		func(item domain.Marquee) string { return item.Name },
		timeCreated,
	)
}

// timeCreated returns the creation time used for Marquee list sorting.
func timeCreated(item domain.Marquee) time.Time {
	return item.CreatedAt
}

// writeStoreErr maps storage failures to Marquee responses.
func writeStoreErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		response.NotFound(w, r, "marquee")
		return
	}
	response.ServerError(w, r, err)
}

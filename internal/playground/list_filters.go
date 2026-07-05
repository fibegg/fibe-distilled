package playground

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// filterPlaygrounds applies supported Playground list filters.
func (h Handler) filterPlaygrounds(r *http.Request, items []domain.Playground) ([]domain.Playground, error) {
	q := r.URL.Query()
	filters, err := h.playgroundFilterParams(r.Context(), q)
	if err != nil {
		return nil, err
	}
	out := selectItems(items, func(item domain.Playground) bool { return matchesPlaygroundFilters(item, filters) })
	return applyListCommon(r, out, listFields[domain.Playground]{
		name:      func(item domain.Playground) string { return item.Name },
		status:    func(item domain.Playground) string { return item.Status },
		createdAt: func(item domain.Playground) time.Time { return item.CreatedAt },
	})
}

// playgroundFilterParams stores parsed Playground list filters.
type playgroundFilterParams struct {
	query             string
	name              string
	status            string
	statusSet         bool
	playspecID        int64
	playspecFilter    bool
	playspecMatchable bool
	marqueeID         int64
	marqueeFilter     bool
	marqueeMatchable  bool
}

// playgroundFilterParams parses Playground list query filters.
func (h Handler) playgroundFilterParams(ctx context.Context, q url.Values) (playgroundFilterParams, error) {
	playspec, err := h.queryPlayspecFilterID(ctx, q)
	if err != nil {
		return playgroundFilterParams{}, err
	}
	marquee, err := h.queryMarqueeFilterID(ctx, q)
	if err != nil {
		return playgroundFilterParams{}, err
	}
	status, err := queryExact(q, "status")
	if err != nil {
		return playgroundFilterParams{}, err
	}
	return playgroundFilterParams{
		query:             q.Get("q"),
		name:              q.Get("name"),
		status:            status.value,
		statusSet:         status.present,
		playspecID:        playspec.id,
		playspecFilter:    playspec.present,
		playspecMatchable: playspec.matchable,
		marqueeID:         marquee.id,
		marqueeFilter:     marquee.present,
		marqueeMatchable:  marquee.matchable,
	}, nil
}

// matchesPlaygroundFilters checks text, status, Playspec, and Marquee filters.
func matchesPlaygroundFilters(item domain.Playground, filters playgroundFilterParams) bool {
	return matchesText(item.Name, filters.query) &&
		matchesText(item.Name, filters.name) &&
		matchesPlaygroundStatus(item, filters) &&
		matchesPlaygroundPlayspec(item, filters) &&
		matchesPlaygroundMarquee(item, filters)
}

// matchesPlaygroundStatus applies an optional exact status filter.
func matchesPlaygroundStatus(item domain.Playground, filters playgroundFilterParams) bool {
	return !filters.statusSet || item.Status == filters.status
}

// matchesPlaygroundPlayspec applies an optional Playspec filter.
func matchesPlaygroundPlayspec(item domain.Playground, filters playgroundFilterParams) bool {
	return matchesOptionalIDFilter(filters.playspecFilter, filters.playspecMatchable, item.PlayspecID, filters.playspecID)
}

// matchesPlaygroundMarquee applies an optional Marquee filter.
func matchesPlaygroundMarquee(item domain.Playground, filters playgroundFilterParams) bool {
	return matchesOptionalIDFilter(filters.marqueeFilter, filters.marqueeMatchable, item.MarqueeID, filters.marqueeID)
}

// matchesOptionalIDFilter applies one optional ID-backed list filter.
func matchesOptionalIDFilter(enabled bool, matchable bool, itemID *int64, expectedID int64) bool {
	if !enabled {
		return true
	}
	return matchable && itemID != nil && *itemID == expectedID
}

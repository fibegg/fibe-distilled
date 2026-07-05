package playspec

import (
	"net/http"
	"net/url"
	"time"

	apilist "github.com/fibegg/fibe-distilled/internal/api/list"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

// filterPlayspecs applies supported Playspec list filters.
func filterPlayspecs(r *http.Request, items []domain.Playspec) ([]domain.Playspec, error) {
	q := r.URL.Query()
	filters, err := filterParams(q)
	if err != nil {
		return nil, err
	}
	filtered := apilist.SelectItems(items, filters.matches)
	return apilist.ApplyCommon(r, filtered, apilist.Fields[domain.Playspec]{
		Name:      func(item domain.Playspec) string { return item.Name },
		CreatedAt: playspecCreatedAt,
	})
}

// filters stores parsed Playspec list filter parameters.
type filters struct {
	query     string
	name      string
	locked    bool
	lockedSet bool
}

// filterParams parses Playspec filter query parameters.
func filterParams(q url.Values) (filters, error) {
	locked, err := apilist.QueryBool(q, "locked")
	if err != nil {
		return filters{}, err
	}
	if _, err := apilist.QueryBool(q, "job_mode"); err != nil {
		return filters{}, err
	}
	return filters{query: q.Get("q"), name: q.Get("name"), locked: locked.Value, lockedSet: locked.Present}, err
}

// matches reports whether a Playspec satisfies parsed filters.
func (f filters) matches(item domain.Playspec) bool {
	return f.matchesLocked(item) &&
		apilist.MatchesText(item.Name, f.query) &&
		apilist.MatchesText(item.Name, f.name)
}

// matchesLocked checks the optional locked filter.
func (f filters) matchesLocked(item domain.Playspec) bool {
	return !f.lockedSet || (item.Locked != nil && *item.Locked == f.locked)
}

// playspecCreatedAt exposes a sortable timestamp for legacy-aware list handling.
func playspecCreatedAt(item domain.Playspec) time.Time {
	if item.CreatedAt == nil {
		return time.Time{}
	}
	return *item.CreatedAt
}

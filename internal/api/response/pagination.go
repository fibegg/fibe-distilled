package response

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Pagination defaults and caps mirror the supported Fibe list contract.
const (
	defaultPage    = 1
	defaultPerPage = 25
	maxPage        = 1000
	maxPerPage     = 100
)

// paginationParams carries parsed list pagination settings.
type paginationParams struct {
	page    int
	perPage int
}

// listPagination parses Fibe-compatible page and per_page query parameters.
func listPagination(r *http.Request) (paginationParams, error) {
	page := defaultPage
	perPage := defaultPerPage
	if r != nil && r.URL != nil {
		query := r.URL.Query()
		var err error
		page, err = positiveCappedQueryInt(query.Get("page"), query.Has("page"), "page", page, maxPage)
		if err != nil {
			return paginationParams{}, err
		}
		perPage, err = positiveCappedQueryInt(query.Get("per_page"), query.Has("per_page"), "per_page", perPage, maxPerPage)
		if err != nil {
			return paginationParams{}, err
		}
	}
	return paginationParams{page: page, perPage: perPage}, nil
}

// positiveCappedQueryInt parses a positive integer query value with an upper cap.
func positiveCappedQueryInt(raw string, present bool, field string, fallback int, capValue int) (int, error) {
	if !present {
		return fallback, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("%s must be a positive integer", field)
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", field)
	}
	if parsed > capValue {
		return capValue, nil
	}
	return parsed, nil
}

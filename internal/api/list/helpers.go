package list

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/api/response"
)

// BoolResult carries a parsed optional boolean query value.
type BoolResult struct {
	// Value is the parsed boolean when Present is true.
	Value bool
	// Present reports whether the query parameter existed.
	Present bool
}

// QueryBool parses a strict boolean query parameter.
func QueryBool(q url.Values, key string) (BoolResult, error) {
	if !q.Has(key) {
		return BoolResult{}, nil
	}
	value := strings.TrimSpace(q.Get(key))
	if value == "" {
		return BoolResult{Present: true}, fmt.Errorf("%s must be true or false", key)
	}
	switch strings.ToLower(value) {
	case "true", "1":
		return BoolResult{Value: true, Present: true}, nil
	case "false", "0":
		return BoolResult{Present: true}, nil
	default:
		return BoolResult{Present: true}, fmt.Errorf("%s must be true or false", key)
	}
}

// StringResult carries a parsed optional string query value.
type StringResult struct {
	// Value is the parsed string when Present is true.
	Value string
	// Present reports whether the query parameter existed.
	Present bool
}

// QueryExact parses a nonblank exact-match query parameter.
func QueryExact(q url.Values, key string) (StringResult, error) {
	if !q.Has(key) {
		return StringResult{}, nil
	}
	value := strings.TrimSpace(q.Get(key))
	if value == "" {
		return StringResult{Present: true}, fmt.Errorf("%s must not be blank", key)
	}
	return StringResult{Value: value, Present: true}, nil
}

// Fields exposes sortable/filterable fields for generic list handling.
type Fields[T any] struct {
	// Name returns the sortable resource name.
	Name func(T) string
	// Status returns the sortable resource status.
	Status func(T) string
	// CreatedAt returns the sortable resource creation time.
	CreatedAt func(T) time.Time
}

// ApplyCommon applies created-at windows and sorting.
func ApplyCommon[T any](r *http.Request, items []T, fields Fields[T]) ([]T, error) {
	q := r.URL.Query()
	window, err := parseCreatedAtWindow(q)
	if err != nil {
		return nil, err
	}
	if window.active() {
		items = SelectItems(items, func(item T) bool {
			return window.includes(fields.CreatedAt(item))
		})
	}
	if err := sortListItems(q, items, fields); err != nil {
		return nil, err
	}
	return items, nil
}

// ApplyNamedCommon applies common list behavior for name/created_at resources.
func ApplyNamedCommon[T any](
	r *http.Request,
	items []T,
	name func(T) string,
	createdAt func(T) time.Time,
) ([]T, error) {
	return ApplyCommon(r, items, Fields[T]{Name: name, CreatedAt: createdAt})
}

// WriteFiltered loads, filters, and writes a list envelope.
func WriteFiltered[T any](
	w http.ResponseWriter,
	r *http.Request,
	load func(context.Context) ([]T, error),
	filter func(*http.Request, []T) ([]T, error),
) {
	items, err := load(r.Context())
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	filtered, err := filter(r, items)
	if err != nil {
		response.BadRequest(w, r, err.Error())
		return
	}
	response.List(w, r, filtered)
}

// SelectItems filters a slice in place.
func SelectItems[T any](items []T, keep func(T) bool) []T {
	out := items[:0]
	for _, item := range items {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}

// MatchesText performs case-insensitive substring matching.
func MatchesText(value string, query string) bool {
	query = strings.TrimSpace(query)
	return query == "" || strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

// CompareString compares strings case-insensitively.
func CompareString(left string, right string) int {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// CompareTime compares timestamps for sorting.
func CompareTime(left time.Time, right time.Time) int {
	switch {
	case left.Before(right):
		return -1
	case left.After(right):
		return 1
	default:
		return 0
	}
}

// createdAtWindow stores optional created_at filter bounds.
type createdAtWindow struct {
	after     time.Time
	before    time.Time
	hasAfter  bool
	hasBefore bool
}

// parseCreatedAtWindow parses created_after and created_before filters.
func parseCreatedAtWindow(q url.Values) (createdAtWindow, error) {
	after, err := parseListTime(q, "created_after")
	if err != nil {
		return createdAtWindow{}, err
	}
	before, err := parseListTime(q, "created_before")
	if err != nil {
		return createdAtWindow{}, err
	}
	return createdAtWindow{after: after.value, before: before.value, hasAfter: after.present, hasBefore: before.present}, nil
}

// active reports whether the created-at window should filter.
func (w createdAtWindow) active() bool {
	return w.hasAfter || w.hasBefore
}

// includes reports whether a timestamp falls within the window.
func (w createdAtWindow) includes(createdAt time.Time) bool {
	if createdAt.IsZero() {
		return false
	}
	if w.hasAfter && createdAt.Before(w.after) {
		return false
	}
	return !w.hasBefore || !createdAt.After(w.before)
}

// listTimeResult carries a parsed optional time query value.
type listTimeResult struct {
	value   time.Time
	present bool
}

// parseListTime parses RFC3339 or YYYY-MM-DD list filters.
func parseListTime(q url.Values, field string) (listTimeResult, error) {
	if !q.Has(field) {
		return listTimeResult{}, nil
	}
	raw := strings.TrimSpace(q.Get(field))
	if raw == "" {
		return listTimeResult{present: true}, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD", field)
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return listTimeResult{value: parsed, present: true}, nil
		}
	}
	return listTimeResult{present: true}, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD", field)
}

// sortListItems applies SDK sort=<column>_<asc|desc>.
func sortListItems[T any](q url.Values, items []T, fields Fields[T]) error {
	parsed, err := parseListSort(q)
	if err != nil || !parsed.present {
		return err
	}
	spec := parsed.value
	if !supportsSortColumn(spec.column, fields) {
		return fmt.Errorf("sort column %q is not supported", spec.column)
	}
	sort.SliceStable(items, func(i, j int) bool {
		cmp := compareListItems(items[i], items[j], spec.column, fields)
		return spec.less(cmp)
	})
	return nil
}

// listSort is a parsed SDK sort query.
type listSort struct {
	column    string
	direction string
}

// listSortResult carries a parsed optional sort query value.
type listSortResult struct {
	value   listSort
	present bool
}

// parseListSort parses sort=<column>_<asc|desc>.
func parseListSort(q url.Values) (listSortResult, error) {
	if !q.Has("sort") {
		return listSortResult{}, nil
	}
	raw := strings.TrimSpace(q.Get("sort"))
	if raw == "" {
		return listSortResult{present: true}, fmt.Errorf("sort direction must be asc or desc")
	}
	spec, ok := splitListSort(raw)
	if !ok {
		return listSortResult{present: true}, fmt.Errorf("sort direction must be asc or desc")
	}
	return listSortResult{value: spec, present: true}, nil
}

// splitListSort separates a sort token into column and direction.
func splitListSort(raw string) (listSort, bool) {
	for _, direction := range []string{"asc", "desc"} {
		suffix := "_" + direction
		if column, ok := strings.CutSuffix(raw, suffix); ok {
			return listSort{column: column, direction: direction}, true
		}
	}
	return listSort{}, false
}

// compareListItems compares two resources by a supported list column.
func compareListItems[T any](left T, right T, column string, fields Fields[T]) int {
	switch column {
	case "name":
		return CompareString(fields.Name(left), fields.Name(right))
	case "status":
		return CompareString(fields.Status(left), fields.Status(right))
	case "created_at":
		return CompareTime(fields.CreatedAt(left), fields.CreatedAt(right))
	default:
		return 0
	}
}

// less reports whether a comparison matches the requested direction.
func (s listSort) less(cmp int) bool {
	if s.direction == "desc" {
		return cmp > 0
	}
	return cmp < 0
}

// supportsSortColumn checks whether a resource exposes a sort column.
func supportsSortColumn[T any](column string, fields Fields[T]) bool {
	switch column {
	case "name":
		return fields.Name != nil
	case "status":
		return fields.Status != nil
	case "created_at":
		return fields.CreatedAt != nil
	default:
		return false
	}
}

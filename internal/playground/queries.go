package playground

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	apilist "github.com/fibegg/fibe-distilled/internal/api/list"
)

// resourceFilterValueResult carries one optional resource filter value.
type resourceFilterValueResult struct {
	value   string
	present bool
}

// resourceFilterValue returns one trimmed resource filter value.
func resourceFilterValue(q url.Values, key string) (resourceFilterValueResult, error) {
	values, present := q[key]
	if !present {
		return resourceFilterValueResult{}, nil
	}
	if len(values) == 0 {
		return resourceFilterValueResult{present: true}, badRequestError{message: key + " must be an id or name"}
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return resourceFilterValueResult{present: true}, badRequestError{message: key + " must be an id or name"}
	}
	return resourceFilterValueResult{value: value, present: true}, nil
}

// positiveResourceFilterIDResult carries a parsed numeric resource filter.
type positiveResourceFilterIDResult struct {
	id      int64
	numeric bool
}

// positiveResourceFilterID parses positive numeric resource filter values.
func positiveResourceFilterID(value string, key string) (positiveResourceFilterIDResult, error) {
	if !looksLikeSignedInteger(value) {
		return positiveResourceFilterIDResult{}, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return positiveResourceFilterIDResult{numeric: true}, badRequestError{message: key + " must be a positive id or name"}
	}
	return positiveResourceFilterIDResult{id: id, numeric: true}, nil
}

// looksLikeSignedInteger reports whether a string should be parsed as an ID.
func looksLikeSignedInteger(value string) bool {
	digits := signedIntegerDigits(value)
	return digits != "" && strings.IndexFunc(digits, nonASCIIDigit) == -1
}

// signedIntegerDigits removes one leading sign from a possible integer.
func signedIntegerDigits(value string) string {
	if value == "" || (value[0] != '+' && value[0] != '-') {
		return value
	}
	return value[1:]
}

// nonASCIIDigit reports whether a rune is outside the API's numeric ID shape.
func nonASCIIDigit(char rune) bool {
	return char < '0' || char > '9'
}

// queryStringResult carries a parsed optional query string.
type queryStringResult struct {
	value   string
	present bool
}

// queryExact parses a nonblank exact-match query parameter.
func queryExact(q url.Values, key string) (queryStringResult, error) {
	parsed, err := apilist.QueryExact(q, key)
	if err != nil {
		return queryStringResult{value: parsed.Value, present: parsed.Present}, badRequestError{message: err.Error()}
	}
	return queryStringResult{value: parsed.Value, present: parsed.Present}, nil
}

// listFields exposes sortable fields for Playground list handling.
type listFields[T any] struct {
	name      func(T) string
	status    func(T) string
	createdAt func(T) time.Time
}

// applyListCommon applies created-at windows and sorting.
func applyListCommon[T any](r *http.Request, items []T, fields listFields[T]) ([]T, error) {
	filtered, err := apilist.ApplyCommon(r, items, apilist.Fields[T]{
		Name:      fields.name,
		Status:    fields.status,
		CreatedAt: fields.createdAt,
	})
	if err != nil {
		return nil, badRequestError{message: err.Error()}
	}
	return filtered, nil
}

// selectItems filters a slice in place.
func selectItems[T any](items []T, keep func(T) bool) []T {
	return apilist.SelectItems(items, keep)
}

// matchesText performs case-insensitive substring matching.
func matchesText(value string, query string) bool {
	return apilist.MatchesText(value, query)
}

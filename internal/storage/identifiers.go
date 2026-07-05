package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// deleteByIdentifier deletes one row by numeric ID or name and requires a hit.
func (s *DB) deleteByIdentifier(ctx context.Context, identifier string, idQuery string, nameQuery string) error {
	query, arg := identifierQuery(identifier, idQuery, nameQuery)
	res, err := s.db.ExecContext(ctx, query, arg)
	return requireRowsAffected(res, err)
}

// requireRowsAffected maps a zero-row mutation to ErrNotFound.
func requireRowsAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// identifierWhere returns the SQL predicate and value for a name-or-ID lookup.
func identifierWhere(identifier string) (string, any) {
	return identifierWhereWithAlias(identifier, "")
}

// identifierQuery chooses the delete/update SQL for a name-or-ID lookup.
func identifierQuery(identifier string, idQuery string, nameQuery string) (string, any) {
	if id, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		return idQuery, id
	}
	return nameQuery, identifier
}

// identifierWhereWithAlias returns a name-or-ID predicate for an optional table alias.
func identifierWhereWithAlias(identifier, alias string) (string, any) {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	if id, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		return prefix + "id=?", id
	}
	return prefix + "name=?", identifier
}

// serviceReferencesPropID checks a services[] prop_id against a Prop ID.
func serviceReferencesPropID(raw any, propID int64) bool {
	if propID <= 0 {
		return false
	}
	servicePropID, ok := positiveInt64FromAny(raw)
	return ok && servicePropID == propID
}

// maxSafeFloatInteger is the largest integer exactly safe in a float64 JSON number.
const maxSafeFloatInteger = 1<<53 - 1

// positiveInt64FromAny parses JSON/YAML scalar values as positive int64 IDs.
func positiveInt64FromAny(v any) (int64, bool) {
	switch typed := v.(type) {
	case int64:
		return positiveInt64(typed)
	case int:
		return positiveInt64(int64(typed))
	case float64:
		return positiveInt64FromFloat(typed)
	case json.Number:
		return positiveInt64FromString(typed.String())
	case string:
		return positiveInt64FromString(typed)
	default:
		return 0, false
	}
}

// positiveInt64FromFloat accepts only precise positive integer float IDs.
func positiveInt64FromFloat(value float64) (int64, bool) {
	if value <= 0 || value > maxSafeFloatInteger || value != math.Trunc(value) {
		return 0, false
	}
	return int64(value), true
}

// positiveInt64FromString parses a positive base-10 int64 ID.
func positiveInt64FromString(value string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, false
	}
	return positiveInt64(n)
}

// positiveInt64 returns a value only when it is greater than zero.
func positiveInt64(n int64) (int64, bool) {
	return n, n > 0
}

package compatgate

import "net/http"

// rejectRepeatedQuery rejects duplicate scalar query parameters.
func rejectRepeatedQuery(r *http.Request) []unsupportedItem {
	if r == nil || r.URL == nil {
		return nil
	}
	var out []unsupportedItem
	for key, values := range r.URL.Query() {
		if len(values) > 1 {
			out = append(out, unsupportedItem{Key: "query:" + key + "[]", Reason: "query parameters in fibe-distilled's supported API contract must be supplied at most once"})
		}
	}
	sortUnsupported(out)
	return out
}

// rejectUnsupportedQuery rejects allowed-but-out-of-scope query toggles.
func rejectUnsupportedQuery(r *http.Request, route routeSpec, op operationSpec) []unsupportedItem {
	if r == nil || r.URL == nil {
		return nil
	}
	out := unsupportedQueryForOperation(route.Resource+":"+op.Operation, r.URL.Query())
	sortUnsupported(out)
	return out
}

// rejectUnknownQuery rejects query keys outside the operation allowlist.
func rejectUnknownQuery(r *http.Request, allowed fieldSet) []unsupportedItem {
	if r == nil || r.URL == nil {
		return nil
	}
	var out []unsupportedItem
	for key := range r.URL.Query() {
		if !allowed[key] {
			out = append(out, unsupportedItem{Key: "query:" + key, Reason: "extra query parameter is not part of fibe-distilled's supported API contract"})
		}
	}
	sortUnsupported(out)
	return out
}

package compatgate

import "net/http"

// rejectUnexpectedBody rejects request bodies for operations without payloads.
func rejectUnexpectedBody(r *http.Request, route routeSpec, op operationSpec) []unsupportedItem {
	if r == nil || !requestBodyPresent(r) || operationMayHaveBody(route, op) {
		return nil
	}
	return []unsupportedItem{{
		Key:    "body",
		Reason: "request body is not part of fibe-distilled's supported API contract for this operation",
	}}
}

// operationMayHaveBody reports supported operations with JSON request payloads.
func operationMayHaveBody(route routeSpec, op operationSpec) bool {
	switch route.Resource + ":" + op.Operation {
	case "props:create",
		"props:update",
		"repo_status_checks:check",
		"playspecs:create",
		"playspecs:update",
		"launches:create",
		"playgrounds:create",
		"playgrounds:update",
		"playgrounds:logs",
		"playgrounds:operation",
		"playgrounds:expiration":
		return true
	default:
		return false
	}
}

// rejectUnknownBodyFields rejects JSON fields outside the operation allowlist.
func rejectUnknownBodyFields(route routeSpec, op operationSpec, body map[string]any) []unsupportedItem {
	if body == nil {
		return nil
	}
	spec := strictBodyFieldSpec(route, op)
	if spec.root != "" {
		return rejectWrappedBodyFields(body, spec.root, spec.allowed, spec.nested)
	}
	return rejectRawBodyFields(body, spec.allowed, spec.nested)
}

// strictBodyFieldSpec returns the body allowlist for an operation.
func strictBodyFieldSpec(route routeSpec, op operationSpec) bodyFieldSpec {
	spec, ok := strictBodyFieldSpecs[route.Resource+":"+op.Operation]
	if ok {
		return spec
	}
	return bodyFieldSpec{allowed: fields()}
}

// rejectWrappedBodyFields validates Rails-style {resource:{...}} payloads.
func rejectWrappedBodyFields(body map[string]any, root string, allowed fieldSet, nested map[string]nestedFieldChecker) []unsupportedItem {
	var out []unsupportedItem
	for key := range body {
		if key != root {
			out = append(out, unsupportedItem{Key: "field:" + key, Reason: "extra top-level JSON field is not part of fibe-distilled's supported API contract; wrap payload fields under " + root})
		}
	}
	raw, ok := body[root]
	if !ok {
		sortUnsupported(out)
		return out
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		sortUnsupported(out)
		return out
	}
	out = append(out, rejectMapFields(payload, allowed, "field:", nested)...)
	sortUnsupported(out)
	return out
}

// rejectRawBodyFields validates payloads without a resource wrapper.
func rejectRawBodyFields(body map[string]any, allowed fieldSet, nested map[string]nestedFieldChecker) []unsupportedItem {
	out := rejectMapFields(body, allowed, "field:", nested)
	sortUnsupported(out)
	return out
}

// rejectMapFields rejects unknown keys and delegates known nested maps.
func rejectMapFields(values map[string]any, allowed fieldSet, prefix string, nested map[string]nestedFieldChecker) []unsupportedItem {
	var out []unsupportedItem
	for key, value := range values {
		if !allowed[key] {
			out = append(out, unsupportedItem{Key: prefix + key, Reason: "extra JSON field is not part of fibe-distilled's supported API contract"})
			continue
		}
		if check := nested[key]; check != nil {
			out = append(out, check(value, prefix+key)...)
		}
	}
	return out
}

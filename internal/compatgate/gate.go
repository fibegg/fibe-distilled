package compatgate

import (
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/details"
)

// Gate classifies API requests before business handlers run.
type Gate struct {
	routes              []routeSpec
	unsupportedRoutes   []unsupportedRoute
	unsupportedSurfaces map[string]unsupportedSurface
}

// Decision describes whether a request may continue or should fail early.
type Decision struct {
	// Allowed reports whether the handler may receive the request.
	Allowed bool
	// Status is the HTTP status for rejected requests.
	Status int
	// Code is the structured fibe-distilled error code.
	Code string
	// Message is the user-facing compatibility explanation.
	Message string
	// Details carries resource, operation, and unsupported-surface metadata.
	Details map[string]any
}

// unsupportedItem is one rejected field, action, query, or route detail.
type unsupportedItem struct {
	Key    string
	Reason string
}

// New builds a Gate from the local compatibility registry.
func New() *Gate {
	return &Gate{
		routes:              routes(),
		unsupportedRoutes:   unsupportedRoutes(),
		unsupportedSurfaces: unsupportedSurfaces(),
	}
}

// Check classifies one request and preserves its body for downstream handlers.
func (g *Gate) Check(r *http.Request) Decision {
	if r == nil || r.URL == nil || !strings.HasPrefix(r.URL.Path, "/api/") {
		return Decision{Allowed: true}
	}
	segments := apiSegments(r.URL.Path)
	if decision, ok := g.unsupportedRouteDecision(r, segments); ok {
		return decision
	}
	if decision, ok := g.supportedRouteDecision(r, segments); ok {
		return decision
	}
	return g.unknownSurfaceDecision(r, segments)
}

// unsupportedRouteDecision checks explicit route denials before generic matches.
func (g *Gate) unsupportedRouteDecision(r *http.Request, segments []string) (Decision, bool) {
	for _, route := range g.unsupportedRoutes {
		if matchUnsupportedRoute(route, r.Method, segments) {
			return unsupportedSurfaceDecision(r, route.Resource, route.Operation, route.Reason, route.Supported), true
		}
	}
	return Decision{}, false
}

// supportedRouteDecision checks the supported route registry.
func (g *Gate) supportedRouteDecision(r *http.Request, segments []string) (Decision, bool) {
	for _, route := range g.routes {
		if !matchSegments(route.Segments, segments) {
			continue
		}
		return checkSupportedRoute(r, route), true
	}
	return Decision{}, false
}

// checkSupportedRoute validates method, params, body fields, and custom checks.
func checkSupportedRoute(r *http.Request, route routeSpec) Decision {
	op, ok := route.Methods[r.Method]
	if !ok {
		return unsupportedMethodDecision(r, route, supportedMethods(route.Methods))
	}
	inspection := inspectableJSONBody(r)
	if inspection.tooLarge {
		return payloadTooLargeDecision(r, route.Resource, op.Operation)
	}
	if unsupported := checkStrictRequest(r, route, op, inspection.body, inspection.parsed); len(unsupported) > 0 {
		return unsupportedItemsDecision(r, route.Resource, op.Operation, unsupported, nil)
	}
	if unsupported := checkUnsupportedComposePayloads(r, route, op, inspection.body, inspection.parsed); len(unsupported) > 0 {
		return unsupportedItemsDecision(r, route.Resource, op.Operation, unsupported, nil)
	}
	if op.Check == nil {
		return Decision{Allowed: true}
	}
	unsupported := op.Check(r, inspection.body)
	if len(unsupported) == 0 {
		return Decision{Allowed: true}
	}
	return unsupportedItemsDecision(r, route.Resource, op.Operation, unsupported, supportedOperations(route, op))
}

// supportedOperations returns operation-level alternatives for error details.
func supportedOperations(route routeSpec, op operationSpec) []string {
	if route.Resource == "playgrounds" && op.Operation == "operation" {
		return supportedPlaygroundActions()
	}
	return nil
}

// unknownSurfaceDecision rejects known unsupported families or unknown APIs.
func (g *Gate) unknownSurfaceDecision(r *http.Request, segments []string) Decision {
	if len(segments) > 0 {
		if surface, ok := g.unsupportedSurfaces[segments[0]]; ok {
			return unsupportedSurfaceDecision(r, surface.Resource, surface.Operation, surface.Reason, surface.Supported)
		}
		if segments[0] == "marquees" {
			return unsupportedSurfaceDecision(r, "marquees", "management", configuredMarqueeReason(), supportedMarqueeDiscovery())
		}
	}
	surface := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if surface == "" {
		surface = "api"
	}
	return unsupportedSurfaceDecision(r, surface, "unknown", surface+" is outside fibe-distilled minimal scope", nil)
}

// matchUnsupportedRoute matches an unsupported route and optional method list.
func matchUnsupportedRoute(route unsupportedRoute, method string, segments []string) bool {
	if !matchSegments(route.Segments, segments) {
		return false
	}
	if len(route.Methods) == 0 {
		return true
	}
	return slices.Contains(route.Methods, method)
}

// payloadTooLargeDecision returns the gate's body-size rejection.
func payloadTooLargeDecision(r *http.Request, resource string, operation string) Decision {
	shape := details.PayloadTooLargeShape(r.Method, r.URL.Path, resource, operation, maxJSONBodyBytes)
	return Decision{
		Allowed: false,
		Status:  http.StatusRequestEntityTooLarge,
		Code:    shape.Code,
		Message: shape.Message,
		Details: shape.Details,
	}
}

// apiSegments splits an /api path into route segments.
func apiSegments(path string) []string {
	path = strings.Trim(strings.TrimPrefix(path, "/api/"), "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

// matchSegments matches literal and :identifier route segments.
func matchSegments(pattern []string, actual []string) bool {
	return slices.EqualFunc(pattern, actual, func(part string, value string) bool {
		if strings.HasPrefix(part, ":") {
			return value != ""
		}
		return part == value
	})
}

// unsupportedMethodDecision rejects unsupported methods on supported resources.
func unsupportedMethodDecision(r *http.Request, route routeSpec, supported []string) Decision {
	shape := details.UnsupportedMethodShape(r.Method, r.URL.Path, route.Resource, supported)
	return Decision{
		Allowed: false,
		Status:  http.StatusNotImplemented,
		Code:    shape.Code,
		Message: shape.Message,
		Details: shape.Details,
	}
}

// unsupportedSurfaceDecision builds a NOT_IMPLEMENTED decision for a surface.
func unsupportedSurfaceDecision(r *http.Request, resource string, operation string, reason string, supported []string) Decision {
	shape := details.UnsupportedSurfaceShape(r.Method, r.URL.Path, resource, operation, reason, supported)
	return Decision{
		Allowed: false,
		Status:  http.StatusNotImplemented,
		Code:    shape.Code,
		Message: shape.Message,
		Details: shape.Details,
	}
}

// unsupportedItemsDecision builds a NOT_IMPLEMENTED decision for rejected items.
func unsupportedItemsDecision(r *http.Request, resource string, operation string, items []unsupportedItem, supported []string) Decision {
	unsupported := make([]string, 0, len(items))
	reasons := make([]string, 0, len(items))
	for _, item := range items {
		unsupported = append(unsupported, item.Key)
		if item.Reason != "" {
			reasons = append(reasons, item.Reason)
		}
	}
	shape := details.UnsupportedItemsShape(r.Method, r.URL.Path, resource, operation, unsupported, reasons, supported)
	return Decision{
		Allowed: false,
		Status:  http.StatusNotImplemented,
		Code:    shape.Code,
		Message: shape.Message,
		Details: shape.Details,
	}
}

// supportedMethods returns a sorted method list for a route.
func supportedMethods(methods map[string]operationSpec) []string {
	out := make([]string, 0, len(methods))
	for method := range methods {
		out = append(out, method)
	}
	sort.Strings(out)
	return out
}

// supportedPlaygroundActions returns action alternatives for Playground operations.
func supportedPlaygroundActions() []string {
	return []string{"hard_restart", "retry_compose", "rollout", "start", "stop"}
}

package details

import "strings"

const (
	// CodeNotImplemented is the stable API code for unsupported Fibe surfaces.
	CodeNotImplemented = "NOT_IMPLEMENTED"
	// CodePayloadTooLarge is the stable API code for over-limit inspected bodies.
	CodePayloadTooLarge = "PAYLOAD_TOO_LARGE"
)

// APIErrorShape contains the wire error fields shared by compatibility checks.
type APIErrorShape struct {
	// Code is the stable client-visible error code.
	Code string
	// Message is the stable client-visible message.
	Message string
	// Details carries structured client-visible error context.
	Details map[string]any
}

// PayloadTooLargeShape builds the gate error for over-limit inspected JSON bodies.
func PayloadTooLargeShape(method string, path string, resource string, operation string, maxBytes int64) APIErrorShape {
	return APIErrorShape{
		Code:    CodePayloadTooLarge,
		Message: "request body exceeds fibe-distilled API body limit",
		Details: map[string]any{
			"method":    method,
			"path":      path,
			"resource":  resource,
			"operation": operation,
			"max_bytes": maxBytes,
			"reason":    "fibe-distilled must fully inspect mutating API JSON bodies before handlers run",
		},
	}
}

// UnsupportedMethodShape builds the gate error for unsupported HTTP methods.
func UnsupportedMethodShape(method string, path string, resource string, supported []string) APIErrorShape {
	return APIErrorShape{
		Code:    CodeNotImplemented,
		Message: method + " " + path + " is not supported by fibe-distilled",
		Details: map[string]any{
			"method":            method,
			"path":              path,
			"resource":          resource,
			"operation":         "method",
			"unsupported":       []string{"method:" + method},
			"supported_methods": supported,
			"reason":            "this HTTP method is not part of fibe-distilled's supported API subset for this resource",
		},
	}
}

// UnsupportedSurfaceShape builds the gate error for unsupported routes/resources.
func UnsupportedSurfaceShape(method string, path string, resource string, operation string, reason string, supported []string) APIErrorShape {
	message := "fibe-distilled does not support " + resource
	if operation != "" && operation != "api" {
		message += " " + operation
	}
	if reason != "" {
		message += ": " + reason
	}
	details := map[string]any{
		"method":      method,
		"path":        path,
		"resource":    resource,
		"operation":   operation,
		"unsupported": []string{resource + ":" + operation},
		"reason":      reason,
	}
	if len(supported) > 0 {
		details["supported"] = supported
	}
	return APIErrorShape{Code: CodeNotImplemented, Message: message, Details: details}
}

// UnsupportedItemsShape builds the gate error for rejected fields/actions/queries.
func UnsupportedItemsShape(method string, path string, resource string, operation string, unsupported []string, reasons []string, supported []string) APIErrorShape {
	reason := strings.Join(DedupeStrings(reasons), "; ")
	message := "fibe-distilled does not support " + resource + " " + operation
	if reason != "" {
		message += ": " + reason
	}
	details := map[string]any{
		"method":      method,
		"path":        path,
		"resource":    resource,
		"operation":   operation,
		"unsupported": unsupported,
		"reason":      reason,
	}
	if len(supported) > 0 {
		details["supported"] = supported
	}
	return APIErrorShape{Code: CodeNotImplemented, Message: message, Details: details}
}

// DedupeStrings de-duplicates strings while preserving first-seen order.
func DedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

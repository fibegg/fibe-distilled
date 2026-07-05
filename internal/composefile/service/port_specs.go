package service

import "strings"

// composePort is the normalized container/protocol part of a Compose port.
type composePort struct {
	container string
	protocol  string
}

// applyPortMappings replaces container ports with requested host mappings.
func applyPortMappings(raw any, mappings []portMapping) []any {
	ports := normalizePorts(raw)
	for _, mapping := range mappings {
		ports = applyPortMapping(ports, mapping)
	}
	return ports
}

// applyPortMapping replaces one container port while preserving protocol.
func applyPortMapping(ports []any, mapping portMapping) []any {
	var originalProtocol string
	next := make([]any, 0, len(ports)+1)
	for _, rawPort := range ports {
		port, ok := parseComposePort(rawPort)
		if ok && port.container == mapping.container {
			if originalProtocol == "" {
				originalProtocol = port.protocol
			}
			continue
		}
		next = append(next, rawPort)
	}
	next = append(next, mapping.host+":"+mapping.container+originalProtocol)
	return next
}

// normalizePorts converts Compose port lists into a mutable list.
func normalizePorts(raw any) []any {
	switch typed := raw.(type) {
	case []any:
		return append([]any(nil), typed...)
	case []string:
		out := make([]any, 0, len(typed))
		for _, value := range typed {
			out = append(out, value)
		}
		return out
	default:
		return []any{}
	}
}

// parseComposePort extracts container port and protocol from one item.
func parseComposePort(raw any) (composePort, bool) {
	switch typed := raw.(type) {
	case string:
		return parseComposePortString(typed)
	case map[string]any:
		return parseComposePortMap(typed)
	case map[any]any:
		normalized, _ := AsMap(typed)
		return parseComposePortMap(normalized)
	default:
		return composePort{}, false
	}
}

// parseComposePortString parses short Compose port syntax.
func parseComposePortString(raw string) (composePort, bool) {
	last := raw
	if idx := strings.LastIndex(last, ":"); idx >= 0 {
		last = last[idx+1:]
	}
	container, protocol, _ := strings.Cut(last, "/")
	container = strings.TrimSpace(container)
	if container == "" {
		return composePort{}, false
	}
	return composePort{container: container, protocol: portProtocolSuffix(protocol)}, true
}

// parseComposePortMap parses long Compose port syntax.
func parseComposePortMap(values map[string]any) (composePort, bool) {
	target, ok := portEndpointText(values["target"])
	if !ok {
		return composePort{}, false
	}
	return composePort{
		container: target,
		protocol:  portProtocolSuffix(values["protocol"]),
	}, true
}

// portProtocolSuffix returns a supported Compose port protocol suffix.
func portProtocolSuffix(raw any) string {
	protocol, ok := raw.(string)
	if !ok {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp", "udp":
		return "/" + strings.ToLower(strings.TrimSpace(protocol))
	default:
		return ""
	}
}

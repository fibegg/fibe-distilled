package service

import "strconv"

// portMapping maps a container port to a requested host port.
type portMapping struct {
	container string
	host      string
}

// normalizePortMappings accepts all API shapes for port mappings.
func normalizePortMappings(raw any) []portMapping {
	switch typed := raw.(type) {
	case map[string]any:
		return portMappingsFromStringMap(typed)
	case map[any]any:
		return portMappingsFromAnyMap(typed)
	case []any:
		return portMappingsFromAnyList(typed)
	case []map[string]any:
		return portMappingsFromStringMapList(typed)
	default:
		return nil
	}
}

// portMappingsFromStringMap converts container-to-host maps.
func portMappingsFromStringMap(values map[string]any) []portMapping {
	keys := sortedStringKeys(values)
	mappings := make([]portMapping, 0, len(keys))
	for _, container := range keys {
		if mapping, ok := newPortMapping(container, values[container]); ok {
			mappings = append(mappings, mapping)
		}
	}
	return mappings
}

// portMappingsFromAnyMap normalizes YAML-decoded map keys.
func portMappingsFromAnyMap(values map[any]any) []portMapping {
	normalized := make(map[string]any, len(values))
	for container, host := range values {
		key, ok := container.(string)
		if !ok {
			continue
		}
		normalized[key] = host
	}
	return portMappingsFromStringMap(normalized)
}

// portMappingsFromAnyList parses list objects with container and host fields.
func portMappingsFromAnyList(values []any) []portMapping {
	mappings := make([]portMapping, 0, len(values))
	for _, rawItem := range values {
		item, ok := AsMap(rawItem)
		if !ok {
			continue
		}
		if mapping, ok := newPortMapping(item["container"], item["host"]); ok {
			mappings = append(mappings, mapping)
		}
	}
	return mappings
}

// portMappingsFromStringMapList parses typed list objects.
func portMappingsFromStringMapList(values []map[string]any) []portMapping {
	mappings := make([]portMapping, 0, len(values))
	for _, item := range values {
		if mapping, ok := newPortMapping(item["container"], item["host"]); ok {
			mappings = append(mappings, mapping)
		}
	}
	return mappings
}

// newPortMapping validates one container/host pair.
func newPortMapping(container any, host any) (portMapping, bool) {
	containerText, containerOK := portEndpointText(container)
	hostText, hostOK := portEndpointText(host)
	if !containerOK || !hostOK {
		return portMapping{}, false
	}
	return portMapping{container: containerText, host: hostText}, true
}

// validPortEndpoint reports whether text is a single TCP/UDP port number.
func validPortEndpoint(text string) bool {
	port, err := strconv.Atoi(text)
	return err == nil && port >= 1 && port <= 65535
}

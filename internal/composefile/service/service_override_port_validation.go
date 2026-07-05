package service

import (
	"strconv"
	"strings"
)

// addInvalidPortMappingContent validates all accepted port mapping shapes.
func addInvalidPortMappingContent(issues *serviceOverrideIssueSet, prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		addInvalidPortMappingMap(issues, prefix, typed)
	case map[any]any:
		addInvalidPortMappingAnyMap(issues, prefix, typed)
	case []any:
		addInvalidPortMappingList(issues, prefix, typed)
	default:
		issues.add(prefix)
	}
}

// addInvalidPortMappingMap validates map-form port mappings.
func addInvalidPortMappingMap(issues *serviceOverrideIssueSet, prefix string, values map[string]any) {
	if len(values) == 0 {
		issues.add(prefix)
		return
	}
	for container, host := range values {
		key := strings.TrimSpace(container)
		if !validPortMappingPair(key, host) {
			issues.add(prefix + "." + key)
		}
	}
}

// addInvalidPortMappingAnyMap validates YAML map-form port mappings.
func addInvalidPortMappingAnyMap(issues *serviceOverrideIssueSet, prefix string, values map[any]any) {
	if len(values) == 0 {
		issues.add(prefix)
		return
	}
	for container, host := range values {
		key, ok := stringMapKey(container)
		if !ok {
			issues.add(prefix + ".<non-string-key>")
			continue
		}
		if !validPortMappingPair(key, host) {
			issues.add(prefix + "." + key)
		}
	}
}

// addInvalidPortMappingList validates object-list port mappings.
func addInvalidPortMappingList(issues *serviceOverrideIssueSet, prefix string, values []any) {
	if len(values) == 0 {
		issues.add(prefix)
		return
	}
	for idx, rawItem := range values {
		itemPrefix := prefix + "." + strconv.Itoa(idx)
		item, ok := AsMap(rawItem)
		if !ok {
			issues.add(itemPrefix)
			continue
		}
		addInvalidPortMappingItem(issues, itemPrefix, item)
	}
}

// validPortMappingPair checks one container-to-host mapping.
func validPortMappingPair(container string, host any) bool {
	_, hostOK := portEndpointText(host)
	return validPortEndpoint(container) && hostOK
}

// addInvalidPortMappingItem validates one list-form port mapping object.
func addInvalidPortMappingItem(issues *serviceOverrideIssueSet, prefix string, item map[string]any) {
	addInvalidPortMappingFields(issues, prefix, item)
	addMissingPortMappingEndpoints(issues, prefix, item)
}

// addInvalidPortMappingFields rejects unknown port-mapping object fields.
func addInvalidPortMappingFields(issues *serviceOverrideIssueSet, prefix string, item map[string]any) {
	for key := range item {
		if key != "container" && key != "host" {
			issues.add(prefix + "." + key)
		}
	}
}

// addMissingPortMappingEndpoints rejects missing or malformed endpoints.
func addMissingPortMappingEndpoints(issues *serviceOverrideIssueSet, prefix string, item map[string]any) {
	for _, key := range []string{"container", "host"} {
		value, ok := item[key]
		if _, valueOK := portEndpointText(value); !ok || !valueOK {
			issues.add(prefix + "." + key)
		}
	}
}

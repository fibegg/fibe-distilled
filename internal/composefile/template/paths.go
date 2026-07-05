package template

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// templatePathPattern matches Fibe's schema-enforced path syntax.
var templatePathPattern = regexp.MustCompile(`^[A-Za-z0-9_./\\\[\]-]+$`)

// templatePathValues returns all path bindings declared by a variable.
func templatePathValues(definition map[string]any) []string {
	var out []string
	if path, ok := definition["path"].(string); ok && path != "" {
		out = append(out, path)
	}
	return appendTemplatePathValues(out, definition["paths"])
}

// appendTemplatePathValues appends path bindings from supported shapes.
func appendTemplatePathValues(out []string, value any) []string {
	switch paths := value.(type) {
	case string:
		if paths != "" {
			out = append(out, paths)
		}
	case []string:
		out = appendNonEmptyStrings(out, paths)
	case []any:
		out = appendStringPathValues(out, paths)
	}
	return out
}

// appendNonEmptyStrings appends nonblank strings to a path list.
func appendNonEmptyStrings(out []string, values []string) []string {
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// appendStringPathValues appends string path values from loose lists.
func appendStringPathValues(out []string, values []any) []string {
	for _, value := range values {
		if path, ok := value.(string); ok && path != "" {
			out = append(out, path)
		}
	}
	return out
}

// validateTemplatePathFields checks path and paths against the public schema shape.
func validateTemplatePathFields(name string, definition map[string]any) []string {
	var errs []string
	if raw, ok := definition["path"]; ok {
		errs = append(errs, validateTemplatePathField(name, "path", raw)...)
	}
	if raw, ok := definition["paths"]; ok {
		errs = append(errs, validateTemplatePathsField(name, raw)...)
	}
	return errs
}

// validateTemplatePathField checks one path field value.
func validateTemplatePathField(name string, field string, raw any) []string {
	path, ok := raw.(string)
	if !ok {
		return []string{fmt.Sprintf("Variable %q %s must be a string", name, field)}
	}
	return validateTemplatePathString(name, field, path)
}

// validateTemplatePathsField checks the multi-path field value.
func validateTemplatePathsField(name string, raw any) []string {
	switch paths := raw.(type) {
	case string:
		return validateTemplatePathString(name, "paths", paths)
	case []string:
		return validateTemplatePathStringList(name, paths)
	case []any:
		return validateTemplatePathAnyList(name, paths)
	default:
		return []string{fmt.Sprintf("Variable %q paths must be a string or an array of strings", name)}
	}
}

// validateTemplatePathStringList checks a typed path list.
func validateTemplatePathStringList(name string, values []string) []string {
	errs := make([]string, 0, len(values))
	for i, value := range values {
		errs = append(errs, validateTemplatePathString(name, fmt.Sprintf("paths[%d]", i), value)...)
	}
	return errs
}

// validateTemplatePathAnyList checks a YAML-decoded path list.
func validateTemplatePathAnyList(name string, values []any) []string {
	errs := make([]string, 0, len(values))
	for i, raw := range values {
		path, ok := raw.(string)
		if !ok {
			errs = append(errs, fmt.Sprintf("Variable %q paths[%d] must be a string", name, i))
			continue
		}
		errs = append(errs, validateTemplatePathString(name, fmt.Sprintf("paths[%d]", i), path)...)
	}
	return errs
}

// validateTemplatePathString checks one schema path string.
func validateTemplatePathString(name string, field string, path string) []string {
	if path == "" {
		return []string{fmt.Sprintf("Variable %q %s must be a non-empty template path", name, field)}
	}
	if !templatePathPattern.MatchString(path) {
		return []string{fmt.Sprintf("Variable %q %s must match Fibe template path syntax", name, field)}
	}
	return nil
}

// splitTemplatePath parses dotted template paths with escaped dots.
func splitTemplatePath(path string) []string {
	var parts []string
	var current strings.Builder
	runes := []rune(path)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '.' {
			current.WriteRune('.')
			i++
			continue
		}
		if runes[i] == '.' {
			addTemplatePathPart(&parts, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(runes[i])
	}
	addTemplatePathPart(&parts, current.String())
	return parts
}

// addTemplatePathPart normalizes one template path segment.
func addTemplatePathPart(parts *[]string, raw string) {
	if raw == "" {
		return
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
		if isUnsignedDigitString(inner) {
			*parts = append(*parts, inner)
			return
		}
	}
	*parts = append(*parts, raw)
}

// setPath writes a variable value into a rendered Compose path.
func setPath(root map[string]any, parts []string, value any) error {
	if len(parts) == 0 {
		return errors.New("path was not found or is not traversable")
	}
	return setMapPath(root, parts, value)
}

// setMapPath writes a value into nested maps, preserving dotted keys.
func setMapPath(current map[string]any, parts []string, value any) error {
	pathKey := longestExistingPathKey(current, parts)
	rest := parts[pathKey.consumed:]
	if len(rest) == 0 {
		current[pathKey.key] = value
		return nil
	}
	child, exists := current[pathKey.key]
	next, err := setPathChild(child, rest, value, exists)
	if err != nil {
		return err
	}
	current[pathKey.key] = next
	return nil
}

// setPathChild descends into or creates the next path container.
func setPathChild(child any, parts []string, value any, exists bool) (any, error) {
	if len(parts) == 0 {
		return value, nil
	}
	if current, ok := asMap(child); ok {
		return current, setMapPath(current, parts, value)
	}
	if current, ok := child.([]any); ok {
		return setSlicePath(current, parts, value)
	}
	if exists {
		return nil, errors.New("path was not found or is not traversable")
	}
	if isUnsignedDigitString(parts[0]) {
		return setSlicePath(nil, parts, value)
	}
	next := map[string]any{}
	if err := setMapPath(next, parts, value); err != nil {
		return nil, err
	}
	return next, nil
}

// setSlicePath writes a value into an indexed list path.
func setSlicePath(current []any, parts []string, value any) ([]any, error) {
	if !isUnsignedDigitString(parts[0]) {
		return nil, errors.New("path was not found or is not traversable")
	}
	index, _ := strconv.Atoi(parts[0])
	exists := index < len(current)
	for len(current) <= index {
		current = append(current, nil)
	}
	if len(parts) == 1 {
		current[index] = value
		return current, nil
	}
	child, err := setPathChild(current[index], parts[1:], value, exists)
	if err != nil {
		return nil, err
	}
	current[index] = child
	return current, nil
}

// pathKeyMatch carries a preserved YAML key and consumed path parts.
type pathKeyMatch struct {
	key      string
	consumed int
}

// longestExistingPathKey preserves YAML keys that themselves contain dots.
func longestExistingPathKey(current map[string]any, parts []string) pathKeyMatch {
	for size := len(parts); size > 1; size-- {
		key := strings.Join(parts[:size], ".")
		if _, ok := current[key]; ok {
			return pathKeyMatch{key: key, consumed: size}
		}
	}
	return pathKeyMatch{key: parts[0], consumed: 1}
}

// stripHostnameFields removes fixed hostnames after template rendering.
func stripHostnameFields(rendered map[string]any) {
	services, ok := asMap(rendered["services"])
	if !ok {
		return
	}
	for name, raw := range services {
		service, ok := asMap(raw)
		if !ok {
			continue
		}
		delete(service, "hostname")
		services[name] = service
	}
}

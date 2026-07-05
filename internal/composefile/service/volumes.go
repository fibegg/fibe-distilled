package service

import (
	"maps"
	"strings"
)

// replaceSourceMount points a service mount at the remote source checkout.
func replaceSourceMount(raw any, target string, source string) any {
	replaced, changed := replaceVolumeTargetFunc(raw, func(src, dst string) (string, bool) {
		if dst == target {
			return source, true
		}
		return src, false
	})
	if changed {
		return replaced
	}
	switch volumes := replaced.(type) {
	case []any:
		return append(volumes, source+":"+target)
	case []string:
		return append(volumes, source+":"+target)
	default:
		return []any{source + ":" + target}
	}
}

// removeSourceMount removes a local source mount when production mode builds instead.
func removeSourceMount(serviceMap map[string]any, target string) {
	updated, changed := removeVolumeTarget(serviceMap["volumes"], target)
	if !changed {
		return
	}
	if emptyVolumeList(updated) {
		delete(serviceMap, "volumes")
		return
	}
	serviceMap["volumes"] = updated
}

// removeVolumeTarget removes volume entries mounted at target.
func removeVolumeTarget(raw any, target string) (any, bool) {
	switch volumes := raw.(type) {
	case []any:
		return removeVolumeItems(volumes, target, volumeItemTarget)
	case []string:
		return removeVolumeItems(volumes, target, shortVolumeTarget)
	default:
		return raw, false
	}
}

// removeVolumeItems removes entries mounted at target from a typed volume list.
func removeVolumeItems[T any](volumes []T, target string, targetOf func(T) string) ([]T, bool) {
	out := make([]T, 0, len(volumes))
	changed := false
	for _, item := range volumes {
		if targetOf(item) == target {
			changed = true
			continue
		}
		out = append(out, item)
	}
	return out, changed
}

// volumeItemTarget returns the mounted target path from a volume item.
func volumeItemTarget(raw any) string {
	switch item := raw.(type) {
	case string:
		return shortVolumeTarget(item)
	default:
		values, ok := AsMap(raw)
		if !ok {
			return ""
		}
		return volumeMapTarget(values)
	}
}

// shortVolumeTarget returns the target path from short volume syntax.
func shortVolumeTarget(spec string) string {
	parts := splitVolume(spec)
	if !parts.ok {
		return strings.TrimSpace(spec)
	}
	if target, _, ok := strings.Cut(parts.rest, ":"); ok {
		return target
	}
	return parts.rest
}

// volumeMapTarget returns the target path from long volume syntax.
func volumeMapTarget(values map[string]any) string {
	target, _ := values["target"].(string)
	return target
}

// emptyVolumeList reports whether a volume list has no entries.
func emptyVolumeList(raw any) bool {
	switch volumes := raw.(type) {
	case []any:
		return len(volumes) == 0
	case []string:
		return len(volumes) == 0
	default:
		return false
	}
}

// replacePathVariables expands FIBE_SERVICES_*_PATH volume sources.
func replacePathVariables(raw any, sourcePaths map[string]string) any {
	for serviceName, source := range sourcePaths {
		variablePrefix := "${FIBE_SERVICES_" + envServiceName(serviceName) + "_PATH"
		replaced, _ := replaceVolumeSourcePrefix(raw, variablePrefix, source)
		raw = replaced
	}
	return raw
}

// replaceVolumeSourcePrefix replaces matching volume source prefixes.
func replaceVolumeSourcePrefix(raw any, prefix string, source string) (any, bool) {
	return replaceVolumeTargetFunc(raw, func(src, _ string) (string, bool) {
		if strings.HasPrefix(src, prefix) {
			return source, true
		}
		return src, false
	})
}

// replaceVolumeTargetFunc rewrites supported volume list forms.
func replaceVolumeTargetFunc(raw any, replace func(source, target string) (string, bool)) (any, bool) {
	switch volumes := raw.(type) {
	case []any:
		return replaceVolumeList(volumes, func(item any) (any, bool) {
			return replaceVolumeItem(item, replace)
		})
	case []string:
		return replaceVolumeList(volumes, func(item string) (string, bool) {
			return replaceVolume(item, replace)
		})
	default:
		return raw, false
	}
}

// replaceVolumeList rewrites a typed volume list while tracking changes.
func replaceVolumeList[T any](volumes []T, replace func(T) (T, bool)) ([]T, bool) {
	out := make([]T, 0, len(volumes))
	changed := false
	for _, item := range volumes {
		updated, didChange := replace(item)
		changed = changed || didChange
		out = append(out, updated)
	}
	return out, changed
}

// replaceVolumeItem rewrites the source side of one supported volume item.
func replaceVolumeItem(raw any, replace func(source, target string) (string, bool)) (any, bool) {
	text, ok := raw.(string)
	if ok {
		return replaceVolume(text, replace)
	}
	values, ok := AsMap(raw)
	if !ok {
		return raw, false
	}
	return replaceVolumeMap(values, replace)
}

// replaceVolume rewrites the source side of one short volume spec.
func replaceVolume(spec string, replace func(source, target string) (string, bool)) (string, bool) {
	parts := splitVolume(spec)
	if !parts.ok {
		target := strings.TrimSpace(spec)
		if target == "" {
			return spec, false
		}
		newSource, changed := replace("", target)
		if !changed {
			return spec, false
		}
		return newSource + ":" + target, true
	}
	target := parts.rest
	mode := ""
	if idx := strings.Index(parts.rest, ":"); idx >= 0 {
		target = parts.rest[:idx]
		mode = parts.rest[idx:]
	}
	newSource, changed := replace(parts.source, target)
	if !changed {
		return spec, false
	}
	return newSource + ":" + target + mode, true
}

// replaceVolumeMap rewrites the source side of one long volume spec.
func replaceVolumeMap(values map[string]any, replace func(source, target string) (string, bool)) (map[string]any, bool) {
	target := volumeMapTarget(values)
	if target == "" {
		return values, false
	}
	source, _ := values["source"].(string)
	newSource, changed := replace(source, target)
	if !changed {
		return values, false
	}
	out := maps.Clone(values)
	out["type"] = "bind"
	out["source"] = newSource
	return out, true
}

// volumeSplit carries the source and remaining fields from a short volume spec.
type volumeSplit struct {
	source string
	rest   string
	ok     bool
}

// splitVolume splits short volume syntax while ignoring colons in ${...}.
func splitVolume(spec string) volumeSplit {
	depth := 0
	index := strings.IndexFunc(spec, func(r rune) bool {
		switch r {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ':':
			return depth == 0
		}
		return false
	})
	if index < 0 {
		return volumeSplit{}
	}
	return volumeSplit{source: spec[:index], rest: spec[index+1:], ok: true}
}

// envServiceName normalizes service names for FIBE_SERVICES_*_PATH.
func envServiceName(serviceName string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(serviceName) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

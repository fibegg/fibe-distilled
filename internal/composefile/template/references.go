package template

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// validateTemplateVariableReferences checks inline and path variable usage.
func validateTemplateVariableReferences(value any, definitions map[string]any) error {
	names := inlineTemplateVariableNames(value)
	serviceNames := templateServiceNames(value)
	missing := missingInlineTemplateVariables(names, definitions)
	unused := unusedTemplateVariables(names, definitions)
	invalidPaths := invalidTemplateServicePaths(definitions, serviceNames)
	if len(missing) == 0 && len(unused) == 0 && len(invalidPaths) == 0 {
		return nil
	}
	var errs []string
	if len(missing) > 0 {
		sort.Strings(missing)
		errs = append(errs, "undeclared template variables: "+strings.Join(missing, ", "))
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		errs = append(errs, "unused template variables: "+strings.Join(unused, ", "))
	}
	if len(invalidPaths) > 0 {
		sort.Strings(invalidPaths)
		errs = append(errs, "template variable paths target missing services: "+strings.Join(invalidPaths, ", "))
	}
	return errors.New(strings.Join(errs, "; "))
}

// missingInlineTemplateVariables returns referenced names without definitions.
func missingInlineTemplateVariables(names map[string]bool, definitions map[string]any) []string {
	var missing []string
	for name := range names {
		if _, ok := definitions[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// unusedTemplateVariables returns definitions not used inline or by path.
func unusedTemplateVariables(names map[string]bool, definitions map[string]any) []string {
	var unused []string
	for key, raw := range definitions {
		name := key
		if name == "" || names[name] {
			continue
		}
		definition, ok := asMap(raw)
		if ok && definitionHasPathBinding(definition) {
			continue
		}
		unused = append(unused, name)
	}
	return unused
}

// invalidTemplateServicePaths returns path bindings targeting missing services.
func invalidTemplateServicePaths(definitions map[string]any, serviceNames map[string]bool) []string {
	invalid := make([]string, 0, len(definitions))
	for name, raw := range definitions {
		invalid = append(invalid, invalidTemplateServicePathItems(name, raw, serviceNames)...)
	}
	return invalid
}

// invalidTemplateServicePathItems returns missing service roots for one variable.
func invalidTemplateServicePathItems(name string, raw any, serviceNames map[string]bool) []string {
	definition, ok := asMap(raw)
	if !ok {
		return nil
	}
	var invalid []string
	for _, path := range templatePathValues(definition) {
		if service := missingTemplateServiceRoot(path, serviceNames); service != "" {
			invalid = append(invalid, fmt.Sprintf("%s:%s", name, service))
		}
	}
	return invalid
}

// inlineTemplateVariableNames collects $$var__/$$random__ names from Compose.
func inlineTemplateVariableNames(value any) map[string]bool {
	out := map[string]bool{}
	var walk func(any)
	walk = func(current any) {
		collectInlineTemplateVariableNames(current, out, walk)
	}
	walk(value)
	return out
}

// collectInlineTemplateVariableNames walks one rendered Compose node.
func collectInlineTemplateVariableNames(current any, out map[string]bool, walk func(any)) {
	switch typed := current.(type) {
	case map[string]any:
		for _, item := range typed {
			walk(item)
		}
	case []any:
		for _, item := range typed {
			walk(item)
		}
	case string:
		addTemplateVariableReferenceNames(out, typed)
	}
}

// addTemplateVariableReferenceNames extracts variable names from one string.
func addTemplateVariableReferenceNames(out map[string]bool, value string) {
	matches := templateVariableReferencePattern.FindAllStringSubmatch(value, -1)
	for _, match := range matches {
		if len(match) > 2 {
			out[match[2]] = true
		}
	}
}

// definitionHasPathBinding reports whether a variable writes by path.
func definitionHasPathBinding(definition map[string]any) bool {
	return len(templatePathValues(definition)) > 0
}

// templateServiceNames returns services present in the rendered Compose root.
func templateServiceNames(value any) map[string]bool {
	out := map[string]bool{}
	root, ok := asMap(value)
	if !ok {
		return out
	}
	services, ok := asMap(root["services"])
	if !ok {
		return out
	}
	for name := range services {
		out[name] = true
	}
	return out
}

// missingTemplateServiceRoot returns the missing service name for a path.
func missingTemplateServiceRoot(path string, services map[string]bool) string {
	parts := splitTemplatePath(path)
	if len(parts) < 2 || parts[0] != "services" {
		return ""
	}
	if services[parts[1]] {
		return ""
	}
	return parts[1]
}

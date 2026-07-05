package composefile

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// unsupportedXFibeRuntimeKey is one Fibe execution setting outside fibe-distilled.
type unsupportedXFibeRuntimeKey struct {
	name   string
	reason string
}

// unsupportedXFibeRuntimeKeys are Fibe execution settings outside fibe-distilled.
var unsupportedXFibeRuntimeKeys = []unsupportedXFibeRuntimeKey{
	{name: "job_mode", reason: "job-mode templates belong to Fibe Tricks and are not implemented in fibe-distilled"},
	{name: "trigger_config", reason: "triggered templates require provider/webhook flows outside fibe-distilled"},
	{name: "schedule_config", reason: "scheduled template execution is outside fibe-distilled"},
	{name: "muti_config", reason: "multi-user template interaction is outside fibe-distilled"},
}

// unsupportedXFibeMetadataToggles are true-only metadata behaviors fibe-distilled skips.
var unsupportedXFibeMetadataToggles = []unsupportedXFibeRuntimeKey{
	{name: "preserve_ports", reason: "preserving raw Compose host ports is outside fibe-distilled"},
	{name: "source_defaults", reason: "source-default auto-fill belongs to full-Fibe source-backed template imports"},
}

// parseDocument decodes a Compose document and verifies it defines services.
func parseDocument(composeYAML string) (*document, error) {
	if strings.TrimSpace(composeYAML) == "" {
		return nil, errors.New("compose_yaml is required")
	}
	var doc document
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		return nil, fmt.Errorf("parse compose yaml: %w", err)
	}
	if err := validateRawComposeShapes(composeYAML); err != nil {
		return nil, err
	}
	if len(doc.Services) == 0 {
		return nil, errors.New("compose services are required")
	}
	for name := range doc.Services {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("compose service name is required")
		}
	}
	return &doc, nil
}

// validateRawComposeShapes rejects malformed raw Compose structures.
func validateRawComposeShapes(composeYAML string) error {
	var rendered map[string]any
	if err := yaml.Unmarshal([]byte(composeYAML), &rendered); err != nil {
		return fmt.Errorf("parse compose yaml: %w", err)
	}
	root, err := composeYAMLRoot(composeYAML)
	if err != nil {
		return err
	}
	if err := validateRawServiceNameKeys(root); err != nil {
		return err
	}
	if err := validateRawXFibeStringKeys(root); err != nil {
		return err
	}
	if err := validateRawXFibeNamespace(rendered); err != nil {
		return err
	}
	return validateRawServiceShapes(rendered)
}

// validateRawServiceNameKeys rejects YAML service keys that are not strings.
func validateRawServiceNameKeys(root *yaml.Node) error {
	services := mappingValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil
	}
	if !mappingKeysAreStrings(services) {
		return errors.New("compose service names must be strings")
	}
	return nil
}

// validateRawXFibeStringKeys rejects non-string keys in Fibe extension blocks.
func validateRawXFibeStringKeys(root *yaml.Node) error {
	xFibe := mappingValue(root, "x-fibe.gg")
	if xFibe == nil || xFibe.Kind != yaml.MappingNode {
		return nil
	}
	if err := validateMappingStringKeys("x-fibe.gg", xFibe); err != nil {
		return err
	}
	if err := validateNestedMappingStringKeys("x-fibe.gg.metadata", mappingValue(xFibe, "metadata")); err != nil {
		return err
	}
	variables := mappingValue(xFibe, "variables")
	if err := validateNestedMappingStringKeys("x-fibe.gg.variables", variables); err != nil {
		return err
	}
	return validateVariableDefinitionStringKeys(variables)
}

// validateNestedMappingStringKeys checks keys only when the node is a mapping.
func validateNestedMappingStringKeys(path string, node *yaml.Node) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	return validateMappingStringKeys(path, node)
}

// validateVariableDefinitionStringKeys checks each x-fibe.gg.variables definition map.
func validateVariableDefinitionStringKeys(variables *yaml.Node) error {
	if variables == nil || variables.Kind != yaml.MappingNode {
		return nil
	}
	for idx := 0; idx+1 < len(variables.Content); idx += 2 {
		name := variables.Content[idx].Value
		definition := variables.Content[idx+1]
		if err := validateNestedMappingStringKeys("x-fibe.gg.variables."+name, definition); err != nil {
			return err
		}
	}
	return nil
}

// validateMappingStringKeys returns an error when a YAML map has non-string keys.
func validateMappingStringKeys(path string, node *yaml.Node) error {
	if mappingKeysAreStrings(node) {
		return nil
	}
	return fmt.Errorf("%s keys must be strings", path)
}

// mappingKeysAreStrings reports whether every YAML mapping key is a string scalar.
func mappingKeysAreStrings(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return true
	}
	for idx := 0; idx < len(node.Content); idx += 2 {
		key := node.Content[idx]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return false
		}
	}
	return true
}

// composeYAMLRoot returns the mapping node for a Compose document.
func composeYAMLRoot(composeYAML string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		return nil, fmt.Errorf("parse compose yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	return doc.Content[0], nil
}

// mappingValue returns a named child value from a YAML mapping node.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		if node.Content[idx].Value == key {
			return node.Content[idx+1]
		}
	}
	return nil
}

// validateRawXFibeNamespace rejects malformed Fibe extension blocks.
func validateRawXFibeNamespace(rendered map[string]any) error {
	namespace, ok, err := rawXFibeNamespace(rendered)
	if err != nil || !ok {
		return err
	}
	if err := validateRawXFibeVariables(namespace); err != nil {
		return err
	}
	if err := validateRawXFibeMetadata(namespace); err != nil {
		return err
	}
	return rejectUnsupportedXFibeRuntimeKeys("x-fibe.gg", namespace)
}

// validateRawXFibeMetadata rejects malformed Fibe metadata blocks.
func validateRawXFibeMetadata(namespace map[string]any) error {
	metadata, err := rawXFibeMetadata(namespace)
	if err != nil {
		return err
	}
	if err := rejectUnsupportedXFibeRuntimeKeys("x-fibe.gg.metadata", metadata); err != nil {
		return err
	}
	if err := validateXFibeMetadataStrings(metadata); err != nil {
		return err
	}
	return rejectUnsupportedXFibeMetadataToggles(metadata)
}

// rawXFibeNamespace returns the root Fibe extension object if present.
func rawXFibeNamespace(rendered map[string]any) (map[string]any, bool, error) {
	raw, ok := rendered["x-fibe.gg"]
	if !ok {
		return nil, false, nil
	}
	namespace, ok := AsMap(raw)
	if !ok {
		return nil, false, errors.New("x-fibe.gg must be an object")
	}
	return namespace, true, nil
}

// validateRawXFibeVariables checks the variables block shape when present.
func validateRawXFibeVariables(namespace map[string]any) error {
	raw, ok := namespace["variables"]
	if !ok {
		return nil
	}
	if _, ok := AsMap(raw); !ok {
		return errors.New("x-fibe.gg.variables must be an object")
	}
	return nil
}

// rawXFibeMetadata returns the metadata object when present.
func rawXFibeMetadata(namespace map[string]any) (map[string]any, error) {
	raw, ok := namespace["metadata"]
	if !ok {
		return map[string]any{}, nil
	}
	metadata, ok := AsMap(raw)
	if !ok {
		return nil, errors.New("x-fibe.gg.metadata must be an object")
	}
	return metadata, nil
}

// validateXFibeMetadataStrings checks known display metadata types.
func validateXFibeMetadataStrings(metadata map[string]any) error {
	for _, key := range []string{"description", "category"} {
		raw, ok := metadata[key]
		if !ok {
			continue
		}
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("x-fibe.gg.metadata.%s must be a string", key)
		}
	}
	return nil
}

// rejectUnsupportedXFibeRuntimeKeys fails on full-Fibe execution settings.
func rejectUnsupportedXFibeRuntimeKeys(prefix string, values map[string]any) error {
	for _, key := range unsupportedXFibeRuntimeKeys {
		if _, ok := values[key.name]; ok {
			return fmt.Errorf("%s.%s is not implemented in fibe-distilled: %s", prefix, key.name, key.reason)
		}
	}
	return nil
}

// rejectUnsupportedXFibeMetadataToggles fails on true full-Fibe metadata toggles.
func rejectUnsupportedXFibeMetadataToggles(metadata map[string]any) error {
	for _, key := range unsupportedXFibeMetadataToggles {
		raw, ok := metadata[key.name]
		if !ok {
			continue
		}
		enabled, ok := raw.(bool)
		if !ok {
			return fmt.Errorf("x-fibe.gg.metadata.%s must be a boolean", key.name)
		}
		if enabled {
			return fmt.Errorf("x-fibe.gg.metadata.%s is not implemented in fibe-distilled: %s", key.name, key.reason)
		}
	}
	return nil
}

// validateRawServiceShapes rejects non-object service definitions.
func validateRawServiceShapes(rendered map[string]any) error {
	services, ok := AsMap(rendered["services"])
	if !ok {
		return nil
	}
	for name, raw := range services {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if _, ok := AsMap(raw); !ok {
			return fmt.Errorf("compose service %q must be a mapping", name)
		}
	}
	return nil
}

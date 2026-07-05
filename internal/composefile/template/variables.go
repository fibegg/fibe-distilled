package template

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ApplyVariables renders x-fibe.gg variable definitions into Compose.
func ApplyVariables(rendered map[string]any, variables map[string]string, readRandomBytes func([]byte) (int, error)) error {
	block := templateVariableBlockFrom(rendered)
	if block.invalidBlock {
		return errors.New("x-fibe.gg.variables must be an object")
	}
	if !block.needsCompilation(rendered, variables) {
		return nil
	}
	declarationErr := joinTemplateDeclarationErrors(validateTemplateVariableDefinitions(block.definitions))
	inlineErr := validateTemplateVariableReferences(rendered, block.definitions)
	values, compileErr := compileTemplateVariableValues(block.definitions, variables, readRandomBytes)
	if err := joinTemplateErrors(declarationErr, compileErr, inlineErr); err != nil {
		return err
	}
	replaceInlineTemplateVariables(rendered, values.inline)
	if err := applyTemplatePathVariables(rendered, block.definitions, values.path); err != nil {
		return err
	}
	stripHostnameFields(rendered)
	if block.hasXFibe {
		removeTemplateVariablesBlock(rendered, block.xFibe)
	}
	return nil
}

// templateVariableBlock is the parsed x-fibe.gg variables section.
type templateVariableBlock struct {
	definitions  map[string]any
	xFibe        map[string]any
	hasXFibe     bool
	hasBlock     bool
	invalidBlock bool
}

// templateVariableBlockFrom reads x-fibe.gg.variables without validating it.
func templateVariableBlockFrom(rendered map[string]any) templateVariableBlock {
	block := templateVariableBlock{definitions: map[string]any{}}
	xFibe, ok := asMap(rendered["x-fibe.gg"])
	if !ok {
		return block
	}
	block.xFibe = xFibe
	block.hasXFibe = true
	rawVariables, ok := xFibe["variables"]
	block.hasBlock = ok
	if !ok {
		return block
	}
	if definitions, ok := asMap(rawVariables); ok {
		block.definitions = definitions
		return block
	}
	block.invalidBlock = true
	return block
}

// needsCompilation reports whether template processing has any work to do.
func (b templateVariableBlock) needsCompilation(rendered map[string]any, variables map[string]string) bool {
	return b.hasBlock ||
		len(variables) > 0 ||
		len(inlineTemplateVariableNames(rendered)) > 0 ||
		containsRootDomainToken(rendered)
}

// applyTemplatePathVariables writes resolved variables into declared paths.
func applyTemplatePathVariables(rendered map[string]any, definitions map[string]any, values map[string]any) error {
	var errs []string
	for name, value := range values {
		appendTemplatePathWriteErrors(&errs, rendered, name, definitions[name], value)
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// appendTemplatePathWriteErrors writes one path variable and records failures.
func appendTemplatePathWriteErrors(errs *[]string, rendered map[string]any, name string, rawDefinition any, value any) {
	definition, ok := asMap(rawDefinition)
	if !ok {
		return
	}
	for _, path := range templatePathValues(definition) {
		if err := setPath(rendered, splitTemplatePath(path), value); err != nil {
			*errs = append(*errs, fmt.Sprintf("Variable %q path %q could not be written: %v", name, path, err))
		}
	}
}

// joinTemplateErrors combines template validation phases into one error.
func joinTemplateErrors(errs ...error) error {
	messages := uniqueTemplateErrorMessages(errs...)
	if len(messages) == 0 {
		return nil
	}
	return errors.New(strings.Join(messages, "; "))
}

// uniqueTemplateErrorMessages returns phase messages without exact duplicates.
func uniqueTemplateErrorMessages(errs ...error) []string {
	var messages []string
	seen := map[string]bool{}
	for _, err := range errs {
		appendTemplateErrorMessages(&messages, seen, err)
	}
	return messages
}

// appendTemplateErrorMessages appends de-duplicated message segments.
func appendTemplateErrorMessages(messages *[]string, seen map[string]bool, err error) {
	if err == nil {
		return
	}
	for message := range strings.SplitSeq(err.Error(), "; ") {
		if message == "" || seen[message] {
			continue
		}
		seen[message] = true
		*messages = append(*messages, message)
	}
}

// removeTemplateVariablesBlock strips variable definitions after rendering.
func removeTemplateVariablesBlock(rendered map[string]any, xFibe map[string]any) {
	delete(xFibe, "variables")
	if len(xFibe) == 0 {
		delete(rendered, "x-fibe.gg")
		return
	}
	rendered["x-fibe.gg"] = xFibe
}

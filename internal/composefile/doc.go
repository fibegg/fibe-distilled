// Package composefile parses, validates, and renders Docker Compose YAML for the
// fibe-distilled runtime.
//
// It owns Fibe label interpretation, service summaries, service override
// primitives, routing labels, source mount paths, and runtime compose
// generation. It does not execute Docker commands.
package composefile

// Package api wires fibe-distilled's HTTP API.
//
// It owns routing, authentication, request decoding, API compatibility
// preflighting, response shaping, and the thin handler layer that translates
// HTTP payloads into domain, storage, composefile, runtime, and worker calls.
package api

// Package runtime executes fibe-distilled's local Docker Compose control plane.
//
// It treats Docker Compose CLI plus the local Docker socket as the runtime
// authority, manages the /opt/fibe filesystem contract, starts Traefik, builds
// images, inspects state, gathers logs, and classifies runtime failures.
package runtime

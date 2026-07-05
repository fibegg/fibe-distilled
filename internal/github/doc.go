// Package github wraps the GitHub API and webhook shapes fibe-distilled needs.
//
// It keeps go-github details behind a small interface-shaped client used for
// repository metadata and branch discovery, and owns push webhook signature and
// payload normalization.
package github

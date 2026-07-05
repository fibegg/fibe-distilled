// Package auth verifies fibe-distilled API bearer tokens.
//
// It owns constant-time token comparison and Authorization header parsing for
// the HTTP boundary while staying independent from server configuration.
package auth

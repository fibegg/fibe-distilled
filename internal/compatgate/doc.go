// Package compatgate classifies authenticated Fibe API requests before they
// reach business handlers.
//
// The gate separates fibe-distilled's supported SDK subset from full Fibe Platform
// surfaces and returns structured NOT_IMPLEMENTED responses for unsupported
// endpoints, methods, actions, and fields.
package compatgate

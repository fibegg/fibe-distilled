// Package request normalizes HTTP request inputs for fibe-distilled handlers.
//
// It owns strict JSON body decoding, optional-body handling, and route path
// parameter lookup across stdlib and chi routers.
package request

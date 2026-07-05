package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

// Authorized reports whether an Authorization header matches the configured API token.
func Authorized(header string, token string) bool {
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	return equalSecret(strings.TrimPrefix(header, "Bearer "), token)
}

// equalSecret compares secrets in constant time after hashing.
func equalSecret(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}

package storage

import (
	"crypto/rand"
	"encoding/hex"
)

// randomHex returns a cryptographically random lowercase hex string.
func randomHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

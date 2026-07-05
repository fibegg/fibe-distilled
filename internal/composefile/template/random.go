package template

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// RandomSecret returns a random hex secret for $$random__ variables.
func RandomSecret(readRandomBytes func([]byte) (int, error)) (string, error) {
	if readRandomBytes == nil {
		readRandomBytes = rand.Read
	}
	var b [16]byte
	n, err := readRandomBytes(b[:])
	if err != nil {
		return "", err
	}
	if n != len(b) {
		return "", fmt.Errorf("random source returned %d bytes, want %d", n, len(b))
	}
	return hex.EncodeToString(b[:]), nil
}

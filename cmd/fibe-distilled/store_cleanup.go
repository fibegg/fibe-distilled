package main

import (
	"errors"
	"fmt"

	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// closeStoreAfterOpenError closes a partially opened store and preserves both errors.
func closeStoreAfterOpenError(st *store.DB, cause error) error {
	if err := st.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("close database: %w", err))
	}
	return cause
}

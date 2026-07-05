package storage

import (
	"context"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// CreateAsync inserts an async operation record.
func (s *DB) CreateAsync(ctx context.Context, op domain.AsyncOperation) (domain.AsyncOperation, error) {
	now := time.Now().UTC()
	op.CreatedAt = now
	op.UpdatedAt = now
	if op.Status == "" {
		op.Status = domain.AsyncQueued
	}
	payload, err := encodeStoredJSON("async_operations.payload_json", op.Payload, "{}")
	if err != nil {
		return op, err
	}
	errJSON, err := encodeAsyncError(op.Error)
	if err != nil {
		return op, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO async_operations (id,status,payload_json,error_json,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
		op.ID, op.Status, payload, errJSON, encodeTime(now), encodeTime(now))
	return op, err
}

// GetAsync fetches an async operation by request ID.
func (s *DB) GetAsync(ctx context.Context, id string) (domain.AsyncOperation, error) {
	return queryOne(ctx, s.db, `SELECT id,status,payload_json,error_json,created_at,updated_at FROM async_operations WHERE id=?`, id, scanAsync)
}

// SaveAsync updates an existing async operation.
func (s *DB) SaveAsync(ctx context.Context, op domain.AsyncOperation) error {
	op.UpdatedAt = time.Now().UTC()
	payload, err := encodeStoredJSON("async_operations.payload_json", op.Payload, "{}")
	if err != nil {
		return err
	}
	errJSON, err := encodeAsyncError(op.Error)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE async_operations SET status=?,payload_json=?,error_json=?,updated_at=? WHERE id=?`,
		op.Status, payload, errJSON, encodeTime(op.UpdatedAt), op.ID)
	return requireRowsAffected(res, err)
}

// RecoverInterruptedAsyncOperations marks in-flight async records as interrupted.
func (s *DB) RecoverInterruptedAsyncOperations(ctx context.Context) (int64, error) {
	now := time.Now().UTC()
	apiErr := domain.APIError{
		Code:    "INTERRUPTED",
		Message: "async operation was interrupted by fibe-distilled restart",
	}
	errJSON, err := encodeStoredJSON("async_operations.error_json", apiErr, "{}")
	if err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE async_operations SET status=?, payload_json='{}', error_json=?, updated_at=? WHERE status IN (?,?)`,
		domain.AsyncError, errJSON, encodeTime(now), domain.AsyncQueued, domain.AsyncRunning)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

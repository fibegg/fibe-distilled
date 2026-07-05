package worker

import (
	"context"
	"log/slog"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// Enqueue persists and starts an in-process async operation.
func (w Worker) Enqueue(ctx context.Context, fn func(context.Context) (map[string]any, *domain.APIError)) (domain.AsyncOperation, error) {
	id := domain.RandomID("req_", 16)
	op := domain.AsyncOperation{
		ID:        id,
		Status:    domain.AsyncQueued,
		StatusURL: "/api/async_requests/" + id,
		Payload:   map[string]any{},
	}
	if _, err := w.DB.CreateAsync(ctx, op); err != nil {
		return op, err
	}
	go w.runAsync(context.WithoutCancel(ctx), op, fn)
	return op, nil
}

// runAsync executes an async operation and records terminal state.
func (w Worker) runAsync(ctx context.Context, op domain.AsyncOperation, fn func(context.Context) (map[string]any, *domain.APIError)) {
	op.Status = domain.AsyncRunning
	op.StatusURL = asyncStatusURL(op.ID)
	if err := w.DB.SaveAsync(ctx, op); err != nil {
		slog.Error("async mark running failed", "operation", op.ID, "error", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			w.finishAsync(ctx, op, nil, &domain.APIError{Code: "INTERNAL_ERROR", Message: "async operation failed unexpectedly"})
		}
	}()
	payload, apiErr := fn(ctx)
	w.finishAsync(ctx, op, payload, apiErr)
}

// finishAsync persists either the operation payload or API error.
func (w Worker) finishAsync(ctx context.Context, op domain.AsyncOperation, payload map[string]any, apiErr *domain.APIError) {
	if apiErr != nil {
		op.Status = domain.AsyncError
		op.Error = apiErr
		op.Payload = map[string]any{}
	} else {
		op.Status = domain.AsyncSuccess
		op.Error = nil
		op.Payload = payload
		if op.Payload == nil {
			op.Payload = map[string]any{}
		}
	}
	op.StatusURL = asyncStatusURL(op.ID)
	if err := w.DB.SaveAsync(ctx, op); err != nil {
		w.finishAsyncPersistenceError(ctx, op, err)
	}
}

// finishAsyncPersistenceError terminalizes a failed success-result save.
func (w Worker) finishAsyncPersistenceError(ctx context.Context, op domain.AsyncOperation, err error) {
	if op.Status == domain.AsyncError {
		slog.Error("async save error result failed", "operation", op.ID, "error", err)
		return
	}
	op.Status = domain.AsyncError
	op.Error = &domain.APIError{
		Code:    "INTERNAL_ERROR",
		Message: "async operation result could not be persisted",
		Details: map[string]any{
			"cause": err.Error(),
		},
	}
	op.Payload = map[string]any{}
	op.StatusURL = asyncStatusURL(op.ID)
	if saveErr := w.DB.SaveAsync(ctx, op); saveErr != nil {
		slog.Error("async save persistence error result failed", "operation", op.ID, "save_error", saveErr, "original_error", err)
	}
}

// asyncStatusURL returns the SDK polling path for an async operation.
func asyncStatusURL(id string) string {
	return "/api/async_requests/" + id
}

package storage

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// interruptedPlayground is the row subset needed for startup recovery.
type interruptedPlayground struct {
	id          int64
	status      string
	stepsJSON   string
	detailsJSON string
}

// scanInterruptedPlayground decodes one interrupted Playground candidate.
func scanInterruptedPlayground(row scanner) (interruptedPlayground, error) {
	var pg interruptedPlayground
	err := row.Scan(&pg.id, &pg.status, &pg.stepsJSON, &pg.detailsJSON)
	return pg, err
}

// interruptedPlaygrounds lists Playgrounds stuck in non-terminal creation states.
func (s *DB) interruptedPlaygrounds(ctx context.Context) ([]interruptedPlayground, error) {
	return queryRows(ctx, s.db, `SELECT id,status,creation_steps_json,error_details_json FROM playgrounds WHERE status IN (?,?) ORDER BY id`, scanInterruptedPlayground,
		domain.StatusPending, domain.StatusInProgress)
}

// RecoverInterruptedPlaygrounds marks pending deployments as interrupted.
func (s *DB) RecoverInterruptedPlaygrounds(ctx context.Context) (recovered int64, err error) {
	pending, err := s.interruptedPlaygrounds(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	values, err := newInterruptedRecoveryValues()
	if err != nil {
		return 0, err
	}
	return s.recoverInterruptedCandidates(ctx, pending, now, values)
}

// newInterruptedRecoveryValues builds static startup-recovery row values.
func newInterruptedRecoveryValues() (interruptedRecoveryValues, error) {
	message := "playground deployment was interrupted by fibe-distilled restart"
	reason := "lifecycle_interrupted"
	stateReasons, err := encodeStoredJSON("playgrounds.state_reasons_json", []string{reason}, "[]")
	return interruptedRecoveryValues{message: message, reason: reason, stateReasons: stateReasons}, err
}

// recoverInterruptedCandidates marks every interrupted candidate it can decode.
func (s *DB) recoverInterruptedCandidates(ctx context.Context, pending []interruptedPlayground, now time.Time, values interruptedRecoveryValues) (int64, error) {
	var recovered int64
	var recoveryErr error
	for _, pg := range pending {
		result, err := s.recoverInterruptedCandidate(ctx, pg, now, values)
		if err != nil {
			if !result.recoverable {
				return recovered, err
			}
			recoveryErr = errors.Join(recoveryErr, err)
			continue
		}
		recovered += result.count
	}
	return recovered, recoveryErr
}

// interruptedRecoveryValues contains the static values written to each row.
type interruptedRecoveryValues struct {
	message      string
	reason       string
	stateReasons string
}

// interruptedRecoveryResult carries one interrupted row recovery outcome.
type interruptedRecoveryResult struct {
	count       int64
	recoverable bool
}

// recoverInterruptedCandidate decodes row JSON and marks one candidate interrupted.
func (s *DB) recoverInterruptedCandidate(ctx context.Context, pg interruptedPlayground, now time.Time, values interruptedRecoveryValues) (interruptedRecoveryResult, error) {
	steps, err := markInterruptedCreationSteps(pg.stepsJSON, now, values.message)
	if err != nil {
		return interruptedRecoveryResult{recoverable: true}, err
	}
	details, err := mergeInterruptedDetails(pg.detailsJSON, pg.status, now, values.message)
	if err != nil {
		return interruptedRecoveryResult{recoverable: true}, err
	}
	count, err := s.recoverInterruptedPlayground(ctx, pg.id, now, values.message, values.reason, values.stateReasons, steps, details)
	return interruptedRecoveryResult{count: count}, err
}

// recoverInterruptedPlayground marks one row failed only if it is still interrupted.
func (s *DB) recoverInterruptedPlayground(ctx context.Context, id int64, now time.Time, message, reason, stateReasons, steps, details string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE playgrounds SET status=?, error_message=?, state_reason=?, state_reasons_json=?, creation_steps_json=?, error_details_json=?, updated_at=? WHERE id=? AND status IN (?,?)`,
		domain.StatusError, message, reason, stateReasons, steps, details, encodeTime(now), id, domain.StatusPending, domain.StatusInProgress)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// markInterruptedCreationSteps marks or appends the failed recovery step.
func markInterruptedCreationSteps(raw string, now time.Time, message string) (string, error) {
	steps := make([]domain.PlaygroundCreationStep, 0, 1)
	if err := decodeStoredJSON(raw, "playgrounds.creation_steps_json", &steps); err != nil {
		return "", err
	}
	completedAt := now.UTC()
	for i, step := range slices.Backward(steps) {
		if step.Status == "running" || step.Status == "pending" {
			steps[i].Status = "error"
			steps[i].CompletedAt = &completedAt
			steps[i].ErrorMessage = message
			return encodeStoredJSON("playgrounds.creation_steps_json", steps, "[]")
		}
	}
	steps = append(steps, domain.PlaygroundCreationStep{
		Name:         "startup_recovery",
		Label:        "Startup recovery",
		Status:       "error",
		StartedAt:    &completedAt,
		CompletedAt:  &completedAt,
		ErrorMessage: message,
	})
	return encodeStoredJSON("playgrounds.creation_steps_json", steps, "[]")
}

// mergeInterruptedDetails records structured startup-recovery failure details.
func mergeInterruptedDetails(raw string, previousStatus string, now time.Time, message string) (string, error) {
	var details map[string]any
	if err := decodeStoredJSON(raw, "playgrounds.error_details_json", &details); err != nil {
		return "", err
	}
	if details == nil {
		details = map[string]any{}
	}
	details["interrupted"] = map[string]any{
		"category":        "lifecycle_interrupted",
		"code":            "INTERRUPTED",
		"message":         message,
		"previous_status": previousStatus,
		"recovered_at":    now.UTC().Format(time.RFC3339Nano),
		"recoverable":     false,
	}
	return encodeStoredJSON("playgrounds.error_details_json", details, "{}")
}

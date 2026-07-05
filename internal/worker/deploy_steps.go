package worker

import (
	"context"
	"errors"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// recordCreationStep records a step only while deployment remains active.
func (w Worker) recordCreationStep(ctx context.Context, pg domain.Playground, name string, status string, stepErr error) (domain.Playground, error) {
	return w.recordCreationStepStrict(ctx, pg, name, status, stepErr)
}

// failDeploymentAfterCreationStep records a failed step, then stores Playground error state.
func (w Worker) failDeploymentAfterCreationStep(ctx context.Context, pg domain.Playground, name string, stepErr error, services []string) (domain.Playground, error) {
	recorded, progressErr := w.recordCreationStep(ctx, pg, name, "error", stepErr)
	failed, failErr := w.failDeployment(ctx, recorded, stepErr, services)
	return failed, errors.Join(failErr, progressErr)
}

// recordCreationStepForStart records the first step only from allowed start states.
func (w Worker) recordCreationStepForStart(ctx context.Context, pg domain.Playground, startStatus string, name string, status string, stepErr error) (domain.Playground, error) {
	return w.recordCreationStepWhen(ctx, pg, name, status, stepErr, func(currentStatus string) bool {
		return currentStatus == startStatus && deploymentStartStatusAllowed(currentStatus)
	})
}

// recordCreationStepStrict records a step only while deployment remains active.
func (w Worker) recordCreationStepStrict(ctx context.Context, pg domain.Playground, name string, status string, stepErr error) (domain.Playground, error) {
	return w.recordCreationStepWhen(ctx, pg, name, status, stepErr, deploymentStatusActive)
}

// recordCreationStepWhen persists a step when the current row passes a status guard.
func (w Worker) recordCreationStepWhen(ctx context.Context, pg domain.Playground, name string, status string, stepErr error, allowed func(string) bool) (domain.Playground, error) {
	now := time.Now().UTC()
	step := newCreationStep(name, status, stepErr, now)
	pg.CreationSteps = upsertCreationStep(pg.CreationSteps, step, now)
	return w.saveDeploymentProgressWhen(ctx, pg, allowed)
}

// newCreationStep builds timestamped progress metadata for one phase.
func newCreationStep(name string, status string, stepErr error, now time.Time) domain.PlaygroundCreationStep {
	step := domain.PlaygroundCreationStep{Name: name, Label: creationStepLabel(name), Status: status}
	if status == "running" {
		step.StartedAt = &now
	}
	if creationStepFinished(status) {
		step.CompletedAt = &now
	}
	if stepErr != nil {
		step.ErrorMessage = stepErr.Error()
	}
	return step
}

// creationStepFinished reports terminal creation-step states.
func creationStepFinished(status string) bool {
	return status == "completed" || status == "error" || status == "skipped"
}

// upsertCreationStep replaces an existing step while preserving start time.
func upsertCreationStep(steps []domain.PlaygroundCreationStep, step domain.PlaygroundCreationStep, now time.Time) []domain.PlaygroundCreationStep {
	for i := range steps {
		if steps[i].Name != step.Name {
			continue
		}
		if steps[i].StartedAt != nil {
			step.StartedAt = steps[i].StartedAt
		} else if step.StartedAt == nil {
			step.StartedAt = &now
		}
		steps[i] = step
		return steps
	}
	if step.StartedAt == nil {
		step.StartedAt = &now
	}
	return append(steps, step)
}

package domain

import (
	"slices"
	"strings"
	"time"
)

// StatusSnapshot converts a Playground row into the SDK polling status shape.
func (p Playground) StatusSnapshot(now time.Time) PlaygroundStatus {
	summary := playgroundCreationStepSummary(p)
	return PlaygroundStatus{
		ID:                   p.ID,
		Name:                 p.Name,
		Status:               p.Status,
		CreationStep:         summary.creationStep,
		CreationStepLabel:    summary.creationStepLabel,
		ErrorStep:            summary.errorStep.name,
		ErrorStepLabel:       summary.errorStep.label,
		CreationSteps:        p.CreationSteps,
		Services:             p.Services,
		ServiceURLs:          p.ServiceURLs,
		BuildStatuses:        p.BuildStatuses,
		ErrorMessage:         p.ErrorMessage,
		ErrorDetails:         p.ErrorDetails,
		NeedsRecreation:      p.NeedsRecreation != nil && *p.NeedsRecreation,
		ExpiresAt:            p.ExpiresAt,
		TimeRemaining:        p.TimeRemaining,
		ExpirationPercentage: p.ExpirationPercentage,
		UpdatedAt:            now.UTC(),
	}
}

// playgroundStepSummary names one projected Playground step.
type playgroundStepSummary struct {
	name  string
	label string
}

// playgroundCreationSummary names SDK polling progress fields.
type playgroundCreationSummary struct {
	creationStep      string
	creationStepLabel string
	errorStep         playgroundStepSummary
}

// playgroundCreationStepSummary chooses progress and error steps for polling.
func playgroundCreationStepSummary(p Playground) playgroundCreationSummary {
	if len(p.CreationSteps) == 0 {
		return emptyPlaygroundCreationStepSummary(p.Status)
	}
	latest := lastPlaygroundCreationStep(p.CreationSteps)
	errorStep := lastPlaygroundErrorStep(p.CreationSteps)
	if playgroundCreationStepFinalized(latest) {
		return playgroundCreationSummary{creationStep: "completed", creationStepLabel: "Completed", errorStep: errorStep}
	}
	return playgroundCreationSummary{creationStep: latest.Name, creationStepLabel: latest.Label, errorStep: errorStep}
}

// emptyPlaygroundCreationStepSummary returns progress without a step ledger.
func emptyPlaygroundCreationStepSummary(status string) playgroundCreationSummary {
	if status == StatusRunning || status == StatusCompleted {
		return playgroundCreationSummary{creationStep: "completed", creationStepLabel: "Completed"}
	}
	if status = strings.TrimSpace(status); status != "" {
		return playgroundCreationSummary{creationStep: status}
	}
	return playgroundCreationSummary{creationStep: "pending"}
}

// lastPlaygroundCreationStep returns the final recorded deployment step.
func lastPlaygroundCreationStep(steps []PlaygroundCreationStep) PlaygroundCreationStep {
	return steps[len(steps)-1]
}

// lastPlaygroundErrorStep returns the most recent failed deployment step.
func lastPlaygroundErrorStep(steps []PlaygroundCreationStep) playgroundStepSummary {
	for _, step := range slices.Backward(steps) {
		if step.Status == "error" {
			return playgroundStepSummary{name: step.Name, label: step.Label}
		}
	}
	return playgroundStepSummary{}
}

// playgroundCreationStepFinalized reports whether deployment fully completed.
func playgroundCreationStepFinalized(step PlaygroundCreationStep) bool {
	return step.Name == "finalize" && step.Status == "completed"
}

package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/details"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

// saveDeploymentProgress saves a Playground while deployment is still active.
func (w Worker) saveDeploymentProgress(ctx context.Context, pg domain.Playground) (domain.Playground, error) {
	return w.saveDeploymentProgressWhen(ctx, pg, deploymentStatusActive)
}

// saveDeploymentProgressWhen saves progress after checking current row status.
func (w Worker) saveDeploymentProgressWhen(ctx context.Context, pg domain.Playground, allowed func(string) bool) (domain.Playground, error) {
	if w.DB == nil || pg.ID == 0 {
		return pg, nil
	}
	current, err := w.DB.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		return pg, err
	}
	if !allowed(current.Status) {
		return current, deploymentSupersededError{PlaygroundID: pg.ID, Status: current.Status}
	}
	pg = preserveLatestDeploymentMetadata(pg, current)
	return w.DB.SavePlayground(ctx, pg)
}

// preserveLatestDeploymentMetadata keeps mutable user metadata during deployment.
func preserveLatestDeploymentMetadata(pg domain.Playground, current domain.Playground) domain.Playground {
	pg.Name = current.Name
	pg.ExpiresAt = current.ExpiresAt
	return pg
}

// requireActiveDeployment reloads the row and rejects superseded deploy work.
func (w Worker) requireActiveDeployment(ctx context.Context, pg domain.Playground) (domain.Playground, error) {
	if w.DB == nil || pg.ID == 0 {
		return pg, nil
	}
	current, err := w.DB.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		return pg, err
	}
	if !deploymentStatusActive(current.Status) {
		return current, deploymentSupersededError{PlaygroundID: pg.ID, Status: current.Status}
	}
	return current, nil
}

// deploymentStatusActive reports statuses that may still be mutated by deploy.
func deploymentStatusActive(status string) bool {
	return status == domain.StatusPending || status == domain.StatusInProgress
}

// deploymentStartStatusAllowed reports statuses that may begin a new deploy.
func deploymentStartStatusAllowed(status string) bool {
	switch status {
	case domain.StatusPending, domain.StatusInProgress, domain.StatusRunning, domain.StatusHasChanges, domain.StatusError, domain.StatusStopped:
		return true
	default:
		return false
	}
}

// deploymentSupersededError stops stale deploy work after row status changes.
type deploymentSupersededError struct {
	PlaygroundID int64
	Status       string
}

// Error describes a deployment abandoned because the row status changed.
func (e deploymentSupersededError) Error() string {
	return fmt.Sprintf("playground %d deployment was superseded by status %q", e.PlaygroundID, e.Status)
}

// creationStepLabel maps internal phase names to SDK-visible labels.
func creationStepLabel(name string) string {
	if label, ok := creationStepLabels[name]; ok {
		return label
	}
	return name
}

// creationStepLabels maps deployment phase identifiers to user-visible labels.
var creationStepLabels = map[string]string{
	"compose_render":     "Render compose",
	"host_prerequisites": "Check host",
	"source_sync":        "Sync sources",
	"builds":             "Build images",
	"compose_deploy":     "Deploy compose",
	"runtime_observe":    "Observe runtime",
	"finalize":           "Finalize",
}

// serviceAuthPasswords extracts per-service basic-auth passwords from metadata.
func serviceAuthPasswords(services map[string]any) map[string]string {
	passwords := map[string]string{}
	for name, raw := range services {
		service, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if password, ok := service["auth_password"].(string); ok && strings.TrimSpace(password) != "" {
			passwords[name] = strings.TrimSpace(password)
		}
	}
	if len(passwords) == 0 {
		return nil
	}
	return passwords
}

// failPlayground marks a Playground error and joins save failure with cause.
func (w Worker) failPlayground(ctx context.Context, pg domain.Playground, err error, services []string) (domain.Playground, error) {
	marked, saveErr := w.markPlaygroundError(ctx, pg, err, services)
	return marked, errors.Join(err, saveErr)
}

// failDeployment records deployment errors only if the deploy still owns the row.
func (w Worker) failDeployment(ctx context.Context, pg domain.Playground, err error, services []string) (domain.Playground, error) {
	latest, activeErr := w.requireActiveDeployment(ctx, pg)
	if activeErr != nil {
		return latest, activeErr
	}
	return w.failPlayground(ctx, latest, err, services)
}

// cleanupSupersededRuntime applies stop/destroy intent after deploy races.
func (w Worker) cleanupSupersededRuntime(ctx context.Context, pg domain.Playground, mq domain.Marquee, project string, err error) (domain.Playground, error) {
	var superseded deploymentSupersededError
	if !errors.As(err, &superseded) {
		return pg, err
	}
	cleanupErr := w.cleanupSupersededRuntimeByStatus(ctx, superseded.Status, mq, project)
	return pg, errors.Join(err, cleanupErr)
}

// cleanupSupersededRuntimeByStatus maps row status to remote cleanup action.
func (w Worker) cleanupSupersededRuntimeByStatus(ctx context.Context, status string, mq domain.Marquee, project string) error {
	switch status {
	case domain.StatusStopping, domain.StatusStopped:
		return w.Runtime.StopCompose(ctx, mq, project)
	case domain.StatusDestroying:
		return w.Runtime.DestroyCompose(ctx, mq, project)
	}
	return nil
}

// markPlaygroundError converts runtime/source errors into Playground state.
func (w Worker) markPlaygroundError(ctx context.Context, pg domain.Playground, err error, services []string) (domain.Playground, error) {
	if syncErr, ok := errors.AsType[sourceSyncError](err); ok {
		return w.savePlaygroundErrorState(ctx, pg, sourceSyncPlaygroundError(syncErr))
	}
	diagnostics := details.ClassifyComposeFailure(err.Error(), services)
	msg := diagnostics.ComposeError
	if strings.TrimSpace(msg) == "" || msg == "unknown error" {
		msg = err.Error()
	}
	return w.savePlaygroundErrorState(ctx, pg, playgroundErrorState{
		message:       msg,
		category:      diagnostics.Category,
		buildWarnings: diagnostics.NextActions,
		errorDetails:  diagnostics.Details(),
	})
}

// playgroundErrorState is the normalized row mutation for Playground errors.
type playgroundErrorState struct {
	message       string
	category      string
	buildWarnings []string
	errorDetails  map[string]any
}

// sourceSyncPlaygroundError converts a source-sync error into Playground state.
func sourceSyncPlaygroundError(syncErr sourceSyncError) playgroundErrorState {
	msg := syncErr.Message
	if msg == "" {
		msg = syncErr.Error()
	}
	return playgroundErrorState{
		message:       msg,
		category:      syncErr.Category,
		buildWarnings: syncErr.NextActions(),
		errorDetails:  map[string]any{"source_sync": syncErr.Details()},
	}
}

// savePlaygroundErrorState persists the normalized Playground error state.
func (w Worker) savePlaygroundErrorState(ctx context.Context, pg domain.Playground, state playgroundErrorState) (domain.Playground, error) {
	pg.Status = domain.StatusError
	pg.ErrorMessage = &state.message
	pg.StateReason = &state.category
	pg.StateReasons = []string{state.category}
	pg.BuildWarnings = state.buildWarnings
	pg.ErrorDetails = state.errorDetails
	var preserveErr error
	pg, preserveErr = w.preserveLatestRuntimeFields(ctx, pg)
	if preserveErr != nil {
		return pg, preserveErr
	}
	saved, saveErr := w.DB.SavePlayground(ctx, pg)
	if saveErr != nil {
		return pg, saveErr
	}
	return saved, nil
}

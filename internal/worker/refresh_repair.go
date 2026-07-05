package worker

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
	"github.com/fibegg/fibe-distilled/internal/playguard"
	"github.com/fibegg/fibe-distilled/internal/runtime"
)

// refreshRuntimeArtifactDrift detects remote file drift after service inspection.
func (w Worker) refreshRuntimeArtifactDrift(ctx context.Context, pg domain.Playground, mq domain.Marquee, now time.Time, refreshStatus string) (domain.PlaygroundStatus, error) {
	if !playguard.ShouldCheckRuntimeArtifactDrift(pg) {
		return statusFromPlayground(pg), nil
	}
	current, active, err := w.currentRefreshPlayground(ctx, pg, refreshStatus)
	if err != nil {
		return statusFromPlayground(pg), err
	}
	if !active {
		return statusFromPlayground(current), nil
	}
	pg = current
	drift, driftErr := w.Runtime.RuntimeArtifactDrift(ctx, mq, *pg.ComposeProject, pg.GeneratedComposeYAML)
	if driftErr != nil {
		return statusFromPlayground(pg), driftErr
	}
	if len(drift) == 0 {
		return statusFromPlayground(pg), nil
	}
	return w.repairRuntimeDrift(ctx, pg, mq, "artifact_drift", now, refreshStatus)
}

// repairRuntimeDrift performs one cooldown-guarded runtime repair attempt.
func (w Worker) repairRuntimeDrift(ctx context.Context, pg domain.Playground, mq domain.Marquee, reason string, now time.Time, refreshStatus string) (domain.PlaygroundStatus, error) {
	current, active, err := w.currentRefreshPlayground(ctx, pg, refreshStatus)
	if err != nil {
		return statusFromPlayground(pg), err
	}
	if !active {
		return statusFromPlayground(current), nil
	}
	pg = current
	if playguard.RuntimeRepairCooldownActive(pg, now) {
		return statusFromPlayground(pg), nil
	}
	repaired, active, repairErr := w.repairRuntimeArtifacts(ctx, pg, mq, reason, now, refreshStatus)
	if !active {
		return statusFromPlayground(repaired), repairErr
	}
	if repairErr == nil {
		return statusFromPlayground(repaired), nil
	}
	refreshed, saveErr := w.markPlaygroundError(ctx, repaired, repairErr, playgroundServiceNames(pg.Services))
	return statusFromPlayground(refreshed), errors.Join(repairErr, saveErr)
}

// repairRuntimeArtifacts records repair intent, resyncs sources, and redeploys.
func (w Worker) repairRuntimeArtifacts(ctx context.Context, pg domain.Playground, mq domain.Marquee, reason string, now time.Time, refreshStatus string) (domain.Playground, bool, error) {
	if pg.ComposeProject == nil {
		return pg, true, errors.New("cannot repair runtime artifacts without compose project")
	}
	pg = playguard.MarkRuntimeRepairStarted(pg, reason, now, defaultRuntimeRepairCooldown)
	synced, err := w.syncRepairSources(ctx, pg, mq, refreshStatus)
	if err != nil || !synced.active || synced.handled {
		return synced.playground, synced.active, err
	}
	return w.redeployRuntimeRepair(ctx, synced.playground, mq, refreshStatus)
}

// repairSourceSyncResult carries source-sync repair state.
type repairSourceSyncResult struct {
	playground domain.Playground
	active     bool
	handled    bool
}

// syncRepairSources preserves user work when source sync detects local changes.
func (w Worker) syncRepairSources(ctx context.Context, pg domain.Playground, mq domain.Marquee, refreshStatus string) (repairSourceSyncResult, error) {
	if pg.PlayspecID == nil {
		return repairSourceSyncResult{playground: pg, active: true}, nil
	}
	ps, err := w.DB.GetPlayspec(ctx, idString(*pg.PlayspecID))
	if err != nil {
		return repairSourceSyncResult{playground: pg, active: true, handled: true}, err
	}
	syncErr := w.syncSources(ctx, mq, *pg.ComposeProject, ps)
	if syncErr == nil {
		return repairSourceSyncResult{playground: pg, active: true}, nil
	}
	if classified, ok := errors.AsType[sourceSyncError](syncErr); ok && classified.PreservesWork() {
		saved, active, err := w.saveRepairSourcePreserved(ctx, pg, classified, refreshStatus)
		return repairSourceSyncResult{playground: saved, active: active, handled: true}, err
	}
	return repairSourceSyncResult{playground: pg, active: true, handled: true}, syncErr
}

// saveRepairSourcePreserved marks a Playground changed instead of overwriting work.
func (w Worker) saveRepairSourcePreserved(ctx context.Context, pg domain.Playground, classified sourceSyncError, refreshStatus string) (domain.Playground, bool, error) {
	pg.Status = domain.StatusHasChanges
	pg.ErrorMessage = new(classified.Message)
	pg.StateReason = new(classified.Category)
	pg.StateReasons = []string{classified.Category}
	pg.BuildWarnings = classified.NextActions()
	pg.ErrorDetails = mergeErrorDetails(pg.ErrorDetails, "source_sync", classified.Details())
	current, active, err := w.currentRefreshPlayground(ctx, pg, refreshStatus)
	if err != nil || !active {
		return current, active, err
	}
	pg = preserveLatestRuntimeFieldsFrom(pg, current)
	saved, saveErr := w.DB.SavePlayground(ctx, pg)
	if saveErr != nil {
		return pg, true, saveErr
	}
	return saved, true, nil
}

// redeployRuntimeRepair reruns Compose using the latest persisted runtime fields.
func (w Worker) redeployRuntimeRepair(ctx context.Context, pg domain.Playground, mq domain.Marquee, refreshStatus string) (domain.Playground, bool, error) {
	current, active, err := w.currentRefreshPlayground(ctx, pg, refreshStatus)
	if err != nil || !active {
		return current, active, err
	}
	pg = preserveLatestRuntimeFieldsFrom(pg, current)
	if err := w.Runtime.DeployCompose(ctx, mq, *pg.ComposeProject, pg.ID, pg.GeneratedComposeYAML); err != nil {
		return pg, true, err
	}
	current, active, err = w.currentRefreshPlayground(ctx, pg, refreshStatus)
	if err != nil {
		return pg, true, err
	}
	if !active {
		cleanupErr := w.cleanupSupersededRuntimeByStatus(ctx, current.Status, mq, *pg.ComposeProject)
		return current, false, cleanupErr
	}
	pg = clearRuntimeRepairError(pg)
	saved, err := w.DB.SavePlayground(ctx, pg)
	if err != nil {
		return pg, true, err
	}
	return saved, true, nil
}

// clearRuntimeRepairError marks a successful repair as running.
func clearRuntimeRepairError(pg domain.Playground) domain.Playground {
	pg.Status = domain.StatusRunning
	pg.ErrorMessage = nil
	pg.StateReason = nil
	pg.StateReasons = nil
	pg.BuildWarnings = nil
	pg.NeedsRecreation = new(false)
	if pg.ErrorDetails != nil {
		delete(pg.ErrorDetails, "source_sync")
	}
	return pg
}

// waitForRuntimeArtifacts hides transient missing files while creation is running.
func waitForRuntimeArtifacts(pg domain.Playground, err error) bool {
	if pg.Status != domain.StatusInProgress || err == nil {
		return false
	}
	return runtimeArtifactsMissing(err)
}

// runtimeArtifactsMissing recognizes Compose errors caused by absent runtime files.
func runtimeArtifactsMissing(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, runtime.ErrRemoteFileMissing) {
		return true
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "no such file or directory") {
		return false
	}
	return strings.Contains(message, "compose.yml") || strings.Contains(message, "can't cd") || strings.Contains(message, optfibe.PlaygroundsPath+"/")
}

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/playguard"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// StartPlayguard starts the reconciliation loop.
func (w Worker) StartPlayguard(ctx context.Context, interval time.Duration) {
	interval = playguard.NormalizeInterval(interval)
	go runPlayguardLoop(ctx, interval, "playguard reconcile", func(now time.Time) error {
		return w.ReconcileOnce(ctx, now.UTC())
	})
}

// runPlayguardLoop executes a Playguard task until context cancellation.
func runPlayguardLoop(ctx context.Context, interval time.Duration, task string, run func(time.Time) error) {
	now := time.Now().UTC()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := run(now); err != nil {
			slog.Error("playguard task failed", "task", task, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case now = <-ticker.C:
		}
	}
}

// ReconcileOnce performs one Playguard reconciliation pass.
func (w Worker) ReconcileOnce(ctx context.Context, now time.Time) error {
	if w.DB == nil {
		return nil
	}
	playgrounds, err := w.DB.ListPlaygrounds(ctx)
	if err != nil {
		return err
	}
	reconcileErrs := w.reconcileErrors(ctx, playgrounds, now)
	if err := w.reconcileOwnedRemoteOrphans(ctx); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	return errors.Join(reconcileErrs...)
}

// reconcileErrors collects per-Playground reconciliation errors.
func (w Worker) reconcileErrors(ctx context.Context, playgrounds []domain.Playground, now time.Time) []error {
	var reconcileErrs []error
	for _, pg := range playgrounds {
		if err := w.reconcileCurrentPlayground(ctx, pg, now); err != nil {
			reconcileErrs = append(reconcileErrs, fmt.Errorf("reconcile playground %s/%d: %w", pg.Name, pg.ID, err))
		}
	}
	return reconcileErrs
}

// reconcileCurrentPlayground reloads a list snapshot before reconciliation.
func (w Worker) reconcileCurrentPlayground(ctx context.Context, listed domain.Playground, now time.Time) error {
	if w.DB == nil {
		return nil
	}
	current, err := w.DB.GetPlayground(ctx, idString(listed.ID))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return w.reconcilePlayground(ctx, current, now)
}

// reconcilePlayground applies expiration, source sync, then runtime refresh.
func (w Worker) reconcilePlayground(ctx context.Context, pg domain.Playground, now time.Time) error {
	if pg.ExpiresAt != nil && now.After(*pg.ExpiresAt) && playguard.ExpirableStatus(pg.Status) {
		return w.enforceExpiration(ctx, pg, now)
	}
	refreshed, err := w.syncPlaygroundSourcesIfNeeded(ctx, pg)
	if err != nil {
		return err
	}
	return w.refreshPlaygroundRuntimeIfNeeded(ctx, refreshed)
}

// syncPlaygroundSourcesIfNeeded updates remote checkouts for active Playgrounds.
func (w Worker) syncPlaygroundSourcesIfNeeded(ctx context.Context, pg domain.Playground) (domain.Playground, error) {
	if w.DB == nil || !playguard.ShouldSyncSources(pg) {
		return pg, nil
	}
	if err := w.syncPlaygroundSources(ctx, pg); err != nil {
		return pg, err
	}
	return w.reloadPlaygroundAfterSourceSync(ctx, pg)
}

// reloadPlaygroundAfterSourceSync returns the current row after remote sync.
func (w Worker) reloadPlaygroundAfterSourceSync(ctx context.Context, pg domain.Playground) (domain.Playground, error) {
	refreshed, err := w.DB.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return domain.Playground{}, nil
		}
		return pg, err
	}
	return refreshed, nil
}

// refreshPlaygroundRuntimeIfNeeded observes runtime state for refreshable rows.
func (w Worker) refreshPlaygroundRuntimeIfNeeded(ctx context.Context, pg domain.Playground) error {
	if playguard.ShouldRefreshRuntime(pg) {
		// Stopped is included so standard Playgrounds whose containers transiently
		// vanish are re-inspected and recover to running once they return.
		if _, err := w.RefreshPlayground(ctx, pg); err != nil {
			if w.refreshFailureWasRecorded(ctx, pg.ID) {
				return nil
			}
			return err
		}
	}
	return nil
}

// reconcileOwnedRemoteOrphans removes managed remote trees no longer represented by SQLite.
func (w Worker) reconcileOwnedRemoteOrphans(ctx context.Context) error {
	if w.DB == nil {
		return nil
	}
	marquee, ok, err := w.remoteOrphanSweepMarquee(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	projects, err := w.Runtime.ListPlaygroundProjects(ctx, marquee)
	if err != nil {
		return fmt.Errorf("list remote playgrounds: %w", err)
	}
	playgrounds, err := w.DB.ListPlaygrounds(ctx)
	if err != nil {
		return err
	}
	return w.reapOwnedRemoteProjects(ctx, marquee, playguard.StaleRemoteProjects(projects, playgrounds, marquee.ID))
}

// remoteOrphanSweepMarquee returns the configured runtime Marquee when present.
func (w Worker) remoteOrphanSweepMarquee(ctx context.Context) (domain.Marquee, bool, error) {
	marquee, ok, err := w.DB.GetRuntimeMarquee(ctx)
	if err != nil || !ok {
		return domain.Marquee{}, ok, err
	}
	return marquee, true, nil
}

// reapOwnedRemoteProjects removes each stale managed project independently.
func (w Worker) reapOwnedRemoteProjects(ctx context.Context, marquee domain.Marquee, projects []string) error {
	var errs []error
	for _, project := range projects {
		if err := w.reapOwnedRemoteProject(ctx, marquee, project); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// reapOwnedRemoteProject deletes one stale managed remote tree.
func (w Worker) reapOwnedRemoteProject(ctx context.Context, marquee domain.Marquee, project string) error {
	if err := w.Runtime.DestroyCompose(ctx, marquee, project); err != nil {
		return fmt.Errorf("reap remote playground %s: %w", project, err)
	}
	return nil
}

// refreshFailureWasRecorded reports whether refresh already persisted an error.
func (w Worker) refreshFailureWasRecorded(ctx context.Context, playgroundID int64) bool {
	refreshed, err := w.DB.GetPlayground(ctx, idString(playgroundID))
	return err == nil && refreshed.Status == domain.StatusError
}

// syncPlaygroundSources runs source sync while guarding against stale rows.
func (w Worker) syncPlaygroundSources(ctx context.Context, pg domain.Playground) error {
	if w.DB == nil {
		return nil
	}
	syncStatus := pg.Status
	current, active, err := w.currentSourceSyncTarget(ctx, pg, syncStatus)
	if err != nil || !active {
		return err
	}
	pg = current
	deps, err := w.playgroundSourceSyncDependencies(ctx, pg)
	if err != nil || !deps.ok {
		return err
	}
	if err := w.runPlaygroundSourceSync(ctx, pg, deps.marquee, deps.playspec); err != nil {
		return w.recordSourceSyncFailure(ctx, pg, err, syncStatus)
	}
	return w.clearResolvedSourceSyncFailure(ctx, pg, syncStatus)
}

// runPlaygroundSourceSync updates source checkouts for the configured project.
func (w Worker) runPlaygroundSourceSync(ctx context.Context, pg domain.Playground, mq domain.Marquee, ps domain.Playspec) error {
	return w.syncSources(ctx, mq, *pg.ComposeProject, ps)
}

// currentSourceSyncTarget reloads and validates a source-sync candidate.
func (w Worker) currentSourceSyncTarget(ctx context.Context, pg domain.Playground, syncStatus string) (domain.Playground, bool, error) {
	current, active, err := w.currentSourceSyncPlayground(ctx, pg, syncStatus)
	if err != nil || !active {
		return current, active, err
	}
	if !playguard.ShouldSyncSources(current) || current.PlayspecID == nil {
		return current, false, nil
	}
	return current, true, nil
}

// sourceSyncDependencies carries resources needed for source synchronization.
type sourceSyncDependencies struct {
	marquee  domain.Marquee
	playspec domain.Playspec
	ok       bool
}

// playgroundSourceSyncDependencies loads Marquee and Playspec for source sync.
func (w Worker) playgroundSourceSyncDependencies(ctx context.Context, pg domain.Playground) (sourceSyncDependencies, error) {
	mq, err := w.runtimeMarqueeForPlayground(ctx, pg)
	if err != nil {
		return sourceSyncDependencies{}, err
	}
	ps, err := w.DB.GetPlayspec(ctx, idString(*pg.PlayspecID))
	if err != nil {
		return sourceSyncDependencies{}, err
	}
	paths, err := sourcePathsForPlayground(pg, ps)
	if err != nil {
		return sourceSyncDependencies{}, err
	}
	if len(paths) == 0 {
		return sourceSyncDependencies{marquee: mq, playspec: ps}, nil
	}
	return sourceSyncDependencies{marquee: mq, playspec: ps, ok: true}, nil
}

// clearResolvedSourceSyncFailure removes prior source-sync warnings after success.
func (w Worker) clearResolvedSourceSyncFailure(ctx context.Context, pg domain.Playground, syncStatus string) error {
	current, active, err := w.currentSourceSyncPlayground(ctx, pg, syncStatus)
	if err != nil || !active {
		return err
	}
	pg = current
	if pg.Status == domain.StatusHasChanges && pg.StateReason != nil && strings.HasPrefix(*pg.StateReason, "source_sync_") {
		pg.Status = domain.StatusRunning
		pg.StateReason = nil
		pg.StateReasons = nil
		pg.BuildWarnings = nil
		if pg.ErrorDetails != nil {
			delete(pg.ErrorDetails, "source_sync")
		}
		_, err = w.DB.SavePlayground(ctx, pg)
		return err
	}
	return nil
}

// recordSourceSyncFailure saves source-sync errors without overwriting user work.
func (w Worker) recordSourceSyncFailure(ctx context.Context, pg domain.Playground, syncFailure error, syncStatus string) error {
	current, active, err := w.currentSourceSyncPlayground(ctx, pg, syncStatus)
	if err != nil || !active {
		return err
	}
	pg = sourceSyncFailurePlayground(current, syncFailure)
	_, err = w.DB.SavePlayground(ctx, pg)
	return err
}

// sourceSyncFailurePlayground applies classified or generic source-sync failure state.
func sourceSyncFailurePlayground(pg domain.Playground, syncFailure error) domain.Playground {
	if syncErr, ok := errors.AsType[sourceSyncError](syncFailure); ok {
		return classifiedSourceSyncFailurePlayground(pg, syncErr)
	}
	return genericSourceSyncFailurePlayground(pg, syncFailure)
}

// classifiedSourceSyncFailurePlayground applies source-sync category details.
func classifiedSourceSyncFailurePlayground(pg domain.Playground, syncErr sourceSyncError) domain.Playground {
	message := firstNonEmpty(syncErr.Message, syncErr.Error())
	pg.ErrorDetails = mergeErrorDetails(pg.ErrorDetails, "source_sync", syncErr.Details())
	pg.ErrorMessage = new(message)
	pg.StateReason = new(syncErr.Category)
	pg.StateReasons = []string{syncErr.Category}
	pg.BuildWarnings = syncErr.NextActions()
	if syncErr.PreservesWork() {
		pg.Status = domain.StatusHasChanges
	} else {
		pg.Status = domain.StatusError
	}
	return pg
}

// genericSourceSyncFailurePlayground applies a generic source-sync failure.
func genericSourceSyncFailurePlayground(pg domain.Playground, syncFailure error) domain.Playground {
	message := syncFailure.Error()
	pg.Status = domain.StatusError
	pg.ErrorMessage = new(message)
	pg.ErrorDetails = mergeErrorDetails(pg.ErrorDetails, "source_sync", map[string]any{"category": "source_sync_failed", "message": message})
	pg.StateReason = new("source_sync_failed")
	pg.StateReasons = []string{"source_sync_failed"}
	return pg
}

// currentSourceSyncPlayground reloads a row and verifies its sync status.
func (w Worker) currentSourceSyncPlayground(ctx context.Context, pg domain.Playground, syncStatus string) (domain.Playground, bool, error) {
	current, err := w.DB.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		return pg, false, err
	}
	return current, current.Status == syncStatus, nil
}

// enforceExpiration destroys or stops expired Playgrounds when safe.
func (w Worker) enforceExpiration(ctx context.Context, pg domain.Playground, now time.Time) error {
	claim := newExpirationClaim(pg)
	pg, active, err := w.currentExpirationPlayground(ctx, claim, now)
	if err != nil || !active {
		return err
	}
	if pg.MarqueeID == nil || pg.ComposeProject == nil {
		return w.markExpiredWithoutRuntime(ctx, pg, claim, now)
	}
	return w.expireRuntimePlayground(ctx, pg, claim, now)
}

// expireRuntimePlayground safely destroys runtime before deleting an expired row.
func (w Worker) expireRuntimePlayground(ctx context.Context, pg domain.Playground, claim expirationClaim, now time.Time) error {
	check, err := w.expirationDirtyCheck(ctx, pg)
	if err != nil {
		return err
	}
	pg, active, err := w.currentExpirationPlayground(ctx, claim, now)
	if err != nil || !active {
		return err
	}
	return w.completeRuntimeExpiration(ctx, pg, claim, now, check)
}

// completeRuntimeExpiration handles dirty check, destroy, and delete completion.
func (w Worker) completeRuntimeExpiration(ctx context.Context, pg domain.Playground, claim expirationClaim, now time.Time, check expirationDirtyCheck) error {
	if check.dirty {
		return w.markExpiredDirty(ctx, pg, check.dirtyPaths)
	}
	if err := w.Runtime.DestroyCompose(ctx, check.marquee, *pg.ComposeProject); err != nil {
		return w.recordExpirationDestroyError(ctx, claim, now, err)
	}
	return w.deleteExpiredPlayground(ctx, claim, now)
}

// markExpiredWithoutRuntime stops an expired row with no remote project.
func (w Worker) markExpiredWithoutRuntime(ctx context.Context, pg domain.Playground, claim expirationClaim, now time.Time) error {
	pg, active, err := w.currentExpirationPlayground(ctx, claim, now)
	if err != nil || !active {
		return err
	}
	pg.Status = domain.StatusStopped
	_, err = w.DB.SavePlayground(ctx, pg)
	return err
}

// expirationDependenciesResult carries runtime dependencies used before destruction.
type expirationDependenciesResult struct {
	marquee  domain.Marquee
	playspec domain.Playspec
}

// expirationDependencies loads runtime dependencies used before destruction.
func (w Worker) expirationDependencies(ctx context.Context, pg domain.Playground) (expirationDependenciesResult, error) {
	mq, err := w.runtimeMarqueeForPlayground(ctx, pg)
	if err != nil {
		return expirationDependenciesResult{}, err
	}
	if pg.PlayspecID == nil {
		return expirationDependenciesResult{marquee: mq}, nil
	}
	ps, err := w.DB.GetPlayspec(ctx, idString(*pg.PlayspecID))
	if err != nil {
		return expirationDependenciesResult{}, err
	}
	return expirationDependenciesResult{marquee: mq, playspec: ps}, nil
}

// expirationDirtyCheck captures whether source work blocks expiration cleanup.
type expirationDirtyCheck struct {
	marquee    domain.Marquee
	dirty      bool
	dirtyPaths []string
}

// expirationDirtyCheck inspects source checkouts before deleting expired runtime.
func (w Worker) expirationDirtyCheck(ctx context.Context, pg domain.Playground) (expirationDirtyCheck, error) {
	deps, err := w.expirationDependencies(ctx, pg)
	if err != nil {
		return expirationDirtyCheck{}, err
	}
	paths, err := sourcePathsForPlayground(pg, deps.playspec)
	if err != nil {
		return expirationDirtyCheck{}, err
	}
	if len(paths) == 0 {
		return expirationDirtyCheck{marquee: deps.marquee}, nil
	}
	dirtyPaths, err := w.Runtime.SourceDirtyPaths(ctx, deps.marquee, *pg.ComposeProject, paths)
	dirty := len(dirtyPaths) > 0
	if err != nil {
		dirty = true
	}
	return expirationDirtyCheck{marquee: deps.marquee, dirty: dirty, dirtyPaths: dirtyPaths}, nil
}

// markExpiredDirty preserves expired Playgrounds with dirty source checkouts.
func (w Worker) markExpiredDirty(ctx context.Context, pg domain.Playground, dirtyPaths []string) error {
	pg.Status = domain.StatusHasChanges
	pg.StateReason = new("expiration_skipped_dirty")
	pg.ErrorDetails = map[string]any{"dirty_paths": dirtyPaths}
	_, err := w.DB.SavePlayground(ctx, pg)
	return err
}

// recordExpirationDestroyError persists runtime destroy failure details.
func (w Worker) recordExpirationDestroyError(ctx context.Context, claim expirationClaim, now time.Time, destroyErr error) error {
	pg, active, err := w.currentExpirationPlayground(ctx, claim, now)
	if err != nil || !active {
		return err
	}
	_, saveErr := w.markPlaygroundError(ctx, pg, destroyErr, playgroundServiceNames(pg.Services))
	return errors.Join(destroyErr, saveErr)
}

// deleteExpiredPlayground removes an expired row after remote destroy succeeds.
func (w Worker) deleteExpiredPlayground(ctx context.Context, claim expirationClaim, now time.Time) error {
	pg, active, err := w.currentExpirationPlayground(ctx, claim, now)
	if err != nil {
		return err
	}
	if !active {
		return w.markDestroyedExpirationSuperseded(ctx, claim)
	}
	return w.DB.DeletePlayground(ctx, idString(pg.ID))
}

// markDestroyedExpirationSuperseded handles rows changed while destroy ran.
func (w Worker) markDestroyedExpirationSuperseded(ctx context.Context, claim expirationClaim) error {
	current, err := w.DB.GetPlayground(ctx, idString(claim.playgroundID))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Status != claim.status || !claim.sameRuntimeIdentity(current) {
		return nil
	}
	current.Status = domain.StatusStopped
	current.StateReason = nil
	current.StateReasons = nil
	_, err = w.DB.SavePlayground(ctx, current)
	return err
}

// currentExpirationPlayground reloads and verifies an expiration claim.
func (w Worker) currentExpirationPlayground(ctx context.Context, claim expirationClaim, now time.Time) (domain.Playground, bool, error) {
	current, err := w.DB.GetPlayground(ctx, idString(claim.playgroundID))
	if errors.Is(err, store.ErrNotFound) {
		return domain.Playground{}, false, nil
	}
	if err != nil {
		return domain.Playground{}, false, err
	}
	return current, claim.matches(current, now), nil
}

// expirationClaim freezes row identity before remote expiration work starts.
type expirationClaim struct {
	playgroundID   int64
	status         string
	expiresAt      time.Time
	hasMarqueeID   bool
	marqueeID      int64
	hasProject     bool
	composeProject string
}

// newExpirationClaim captures the fields that make expiration work current.
func newExpirationClaim(pg domain.Playground) expirationClaim {
	claim := expirationClaim{playgroundID: pg.ID, status: pg.Status}
	if pg.ExpiresAt != nil {
		claim.expiresAt = *pg.ExpiresAt
	}
	if pg.MarqueeID != nil {
		claim.hasMarqueeID = true
		claim.marqueeID = *pg.MarqueeID
	}
	if pg.ComposeProject != nil {
		claim.hasProject = true
		claim.composeProject = *pg.ComposeProject
	}
	return claim
}

// matches reports whether a row still matches the expiration claim.
func (c expirationClaim) matches(pg domain.Playground, now time.Time) bool {
	if pg.ID != c.playgroundID || pg.Status != c.status || pg.ExpiresAt == nil {
		return false
	}
	if !playguard.ExpirableStatus(pg.Status) || !pg.ExpiresAt.Equal(c.expiresAt) || !now.After(*pg.ExpiresAt) {
		return false
	}
	return c.sameRuntimeIdentity(pg)
}

// sameRuntimeIdentity compares Marquee/project identity in an expiration claim.
func (c expirationClaim) sameRuntimeIdentity(pg domain.Playground) bool {
	if c.hasMarqueeID != (pg.MarqueeID != nil) || c.hasProject != (pg.ComposeProject != nil) {
		return false
	}
	if c.hasMarqueeID && *pg.MarqueeID != c.marqueeID {
		return false
	}
	return !c.hasProject || *pg.ComposeProject == c.composeProject
}

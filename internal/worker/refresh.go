package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/playguard"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// RefreshPlayground observes remote runtime state and repairs safe drift.
func (w Worker) RefreshPlayground(ctx context.Context, pg domain.Playground) (domain.PlaygroundStatus, error) {
	prepared, err := w.prepareRuntimeRefresh(ctx, pg)
	if refreshBlocked(prepared.active, err) {
		return prepared.status, err
	}
	now := time.Now().UTC()
	inspected, err := w.inspectRuntimeRefreshServices(ctx, prepared.target, now)
	if refreshPhaseDone(inspected.done, err) {
		return inspected.status, err
	}
	observed, err := w.currentObservedRuntimeTarget(ctx, prepared.target)
	if refreshPhaseDone(observed.done, err) {
		return observed.status, err
	}
	target := observed.target
	observation, err := w.applyRuntimeServiceObservation(ctx, target.Playground, target.Marquee, inspected.services, now, target.RefreshStatus)
	if refreshPhaseDone(observation.Done, err) {
		return observation.Status, err
	}
	return w.refreshRuntimeArtifactDrift(ctx, observation.Playground, target.Marquee, now, observation.RefreshStatus)
}

// runtimeRefreshInspection carries one remote service inspection phase result.
type runtimeRefreshInspection struct {
	services []domain.PlaygroundServiceInfo
	status   domain.PlaygroundStatus
	done     bool
}

// inspectRuntimeRefreshServices reads Compose services and handles inspect failures.
func (w Worker) inspectRuntimeRefreshServices(ctx context.Context, target runtimeRefreshTarget, now time.Time) (runtimeRefreshInspection, error) {
	services, inspectErr := w.Runtime.InspectServices(ctx, target.Marquee, *target.Playground.ComposeProject)
	if inspectErr != nil {
		status, err := w.handleRuntimeInspectError(ctx, target.Playground, target.Marquee, inspectErr, now, target.RefreshStatus)
		return runtimeRefreshInspection{status: status, done: true}, err
	}
	return runtimeRefreshInspection{services: services}, nil
}

// runtimeRefreshTarget carries the row and Marquee selected for refresh.
type runtimeRefreshTarget struct {
	Playground    domain.Playground
	Marquee       domain.Marquee
	RefreshStatus string
}

// preparedRuntimeRefresh carries the selected refresh target or final status.
type preparedRuntimeRefresh struct {
	target runtimeRefreshTarget
	status domain.PlaygroundStatus
	active bool
}

// prepareRuntimeRefresh reloads the row and Marquee needed for runtime refresh.
func (w Worker) prepareRuntimeRefresh(ctx context.Context, pg domain.Playground) (preparedRuntimeRefresh, error) {
	if !playguard.CanRefreshRuntime(pg, w.DB != nil) {
		return preparedRuntimeRefresh{status: statusFromPlayground(pg)}, nil
	}
	refreshStatus := pg.Status
	current, active, err := w.currentRuntimeRefreshTarget(ctx, pg, refreshStatus)
	if err != nil {
		return preparedRuntimeRefresh{status: statusFromPlayground(pg)}, err
	}
	if !active {
		return preparedRuntimeRefresh{status: statusFromPlayground(current)}, nil
	}
	mq, err := w.runtimeMarqueeForPlayground(ctx, current)
	if err != nil {
		return preparedRuntimeRefresh{status: statusFromPlayground(current)}, err
	}
	return preparedRuntimeRefresh{target: runtimeRefreshTarget{Playground: current, Marquee: mq, RefreshStatus: refreshStatus}, active: true}, nil
}

// observedRuntimeTarget carries a reloaded target or final status.
type observedRuntimeTarget struct {
	target runtimeRefreshTarget
	status domain.PlaygroundStatus
	done   bool
}

// currentObservedRuntimeTarget reloads the row after remote inspection.
func (w Worker) currentObservedRuntimeTarget(ctx context.Context, target runtimeRefreshTarget) (observedRuntimeTarget, error) {
	current, active, err := w.currentRefreshPlayground(ctx, target.Playground, target.RefreshStatus)
	if err != nil {
		return observedRuntimeTarget{target: target, status: statusFromPlayground(target.Playground), done: true}, err
	}
	if !active {
		return observedRuntimeTarget{target: target, status: statusFromPlayground(current), done: true}, nil
	}
	target.Playground = current
	return observedRuntimeTarget{target: target}, nil
}

// currentRuntimeRefreshTarget reloads a Playground before touching remote state.
func (w Worker) currentRuntimeRefreshTarget(ctx context.Context, pg domain.Playground, refreshStatus string) (domain.Playground, bool, error) {
	current, active, err := w.currentRefreshPlayground(ctx, pg, refreshStatus)
	if err != nil || !active {
		return current, active, err
	}
	if !playguard.CanRefreshRuntime(current, true) {
		if playguard.CanRefreshRuntime(pg, true) {
			return current, false, fmt.Errorf("playground runtime dependency changed before refresh: %w", store.ErrNotFound)
		}
		return current, false, nil
	}
	return current, true, nil
}

// handleRuntimeInspectError turns inspect failures into wait, repair, or error states.
func (w Worker) handleRuntimeInspectError(ctx context.Context, pg domain.Playground, mq domain.Marquee, inspectErr error, now time.Time, refreshStatus string) (domain.PlaygroundStatus, error) {
	current, active, err := w.currentRefreshPlayground(ctx, pg, refreshStatus)
	if err != nil {
		return statusFromPlayground(pg), err
	}
	if !active {
		return statusFromPlayground(current), nil
	}
	pg = current
	if waitForRuntimeArtifacts(pg, inspectErr) {
		return statusFromPlayground(pg), nil
	}
	if playguard.ShouldRepairRuntimeArtifacts(pg, runtimeArtifactsMissing(inspectErr)) {
		return w.repairRuntimeDrift(ctx, pg, mq, "runtime_artifacts_missing", now, refreshStatus)
	}
	refreshed, saveErr := w.markPlaygroundError(ctx, pg, inspectErr, playgroundServiceNames(pg.Services))
	return statusFromPlayground(refreshed), errors.Join(inspectErr, saveErr)
}

// currentRefreshPlayground reloads a row and detects superseded refresh work.
func (w Worker) currentRefreshPlayground(ctx context.Context, pg domain.Playground, refreshStatus string) (domain.Playground, bool, error) {
	if w.DB == nil || pg.ID == 0 {
		return pg, true, nil
	}
	current, err := w.DB.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		return pg, false, err
	}
	return current, current.Status == refreshStatus, nil
}

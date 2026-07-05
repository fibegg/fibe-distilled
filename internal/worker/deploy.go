package worker

import (
	"context"
	"errors"
	"time"

	"github.com/fibegg/fibe-distilled/internal/buildrecord"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

// DeployPlayground renders, builds, syncs, deploys, and records a Playground.
func (w Worker) DeployPlayground(ctx context.Context, pg domain.Playground, ps domain.Playspec, mq *domain.Marquee) (domain.Playground, error) {
	if mq == nil {
		return pg, errors.New("marquee is required for playground deployment")
	}
	now := time.Now().UTC()
	startStatus := pg.Status
	pg.Status = domain.StatusInProgress
	pg.LastAppliedAt = &now
	var err error
	pg, err = w.recordCreationStepForStart(ctx, pg, startStatus, "compose_render", "running", nil)
	if err != nil {
		return pg, err
	}
	render, err := w.renderPlayground(ctx, pg, ps, *mq)
	if err != nil {
		return render.pg, err
	}
	pg = render.pg
	if pg, err = w.saveDeploymentProgress(ctx, pg); err != nil {
		return pg, err
	}
	pg, err = w.deployRenderedPlayground(ctx, pg, render.playspec, *mq, render.project, render.composeYAML, render.services)
	if err != nil {
		return pg, err
	}
	pg = successfulDeploymentPlayground(pg)
	return w.recordCreationStepStrict(ctx, pg, "finalize", "completed", nil)
}

// successfulDeploymentPlayground clears stale lifecycle diagnostics after deploy.
func successfulDeploymentPlayground(pg domain.Playground) domain.Playground {
	pg.Status = domain.StatusRunning
	pg.ErrorMessage = nil
	pg.StateReason = nil
	pg.StateReasons = nil
	pg.BuildWarnings = nil
	pg.ErrorDetails = nil
	return pg
}

// deployRenderedPlayground runs host checks, source sync, builds, and Compose deploy.
func (w Worker) deployRenderedPlayground(ctx context.Context, pg domain.Playground, ps domain.Playspec, mq domain.Marquee, project string, rendered string, services []domain.PlaygroundServiceInfo) (domain.Playground, error) {
	serviceNames := playgroundServiceNames(services)
	var err error
	pg, err = w.runCreationStep(ctx, pg, "host_prerequisites", serviceNames, func(domain.Playground) error {
		return w.Runtime.EnsurePrerequisites(ctx, mq)
	})
	if err != nil {
		return pg, err
	}
	pg, err = w.runCreationStep(ctx, pg, "source_sync", serviceNames, func(active domain.Playground) error {
		if err := w.Runtime.PreparePlaygroundWorkspace(ctx, mq, project, active.ID); err != nil {
			return err
		}
		return w.syncSources(ctx, mq, project, ps)
	})
	if err != nil {
		return pg, err
	}
	buildResult, err := w.buildRenderedPlayground(ctx, pg, ps, mq, rendered, serviceNames)
	if err != nil {
		return buildResult.playground, err
	}
	return w.deployAndObserveCompose(ctx, buildResult.playground, mq, project, buildResult.composeYAML, services, serviceNames)
}

// runCreationStep records, executes, and completes one deployment creation step.
func (w Worker) runCreationStep(ctx context.Context, pg domain.Playground, step string, serviceNames []string, run func(domain.Playground) error) (domain.Playground, error) {
	var err error
	pg, err = w.recordCreationStep(ctx, pg, step, "running", nil)
	if err != nil {
		return pg, err
	}
	if pg, err := w.requireActiveDeployment(ctx, pg); err != nil {
		return pg, err
	}
	if err := run(pg); err != nil {
		return w.failDeploymentAfterCreationStep(ctx, pg, step, err, serviceNames)
	}
	return w.recordCreationStep(ctx, pg, step, "completed", nil)
}

// builtRenderedPlayground carries build-patched Compose and row state.
type builtRenderedPlayground struct {
	composeYAML string
	playground  domain.Playground
}

// buildRenderedPlayground builds remote images and patches rendered Compose refs.
func (w Worker) buildRenderedPlayground(ctx context.Context, pg domain.Playground, ps domain.Playspec, mq domain.Marquee, rendered string, serviceNames []string) (builtRenderedPlayground, error) {
	var err error
	pg, err = w.recordCreationStep(ctx, pg, "builds", "running", nil)
	if err != nil {
		return builtRenderedPlayground{composeYAML: rendered, playground: pg}, err
	}
	if pg, err := w.requireActiveDeployment(ctx, pg); err != nil {
		return builtRenderedPlayground{composeYAML: rendered, playground: pg}, err
	}
	builds, err := w.buildServices(ctx, pg, ps, mq)
	if err != nil {
		pg.BuildStatuses = builds.statuses
		failed, failErr := w.failDeploymentAfterCreationStep(ctx, pg, "builds", err, serviceNames)
		return builtRenderedPlayground{composeYAML: rendered, playground: failed}, failErr
	}
	pg.BuildStatuses = builds.statuses
	renderedWithImages, err := buildrecord.ApplyBuildImages(rendered, builds.imageRefs)
	if err != nil {
		failed, failErr := w.failDeploymentAfterCreationStep(ctx, pg, "builds", err, serviceNames)
		return builtRenderedPlayground{composeYAML: rendered, playground: failed}, failErr
	}
	pg.GeneratedComposeYAML = renderedWithImages
	saved, err := w.saveDeploymentProgress(ctx, pg)
	if err != nil {
		return builtRenderedPlayground{composeYAML: renderedWithImages, playground: pg}, err
	}
	pg, err = w.recordCreationStep(ctx, saved, "builds", "completed", nil)
	if err != nil {
		return builtRenderedPlayground{composeYAML: renderedWithImages, playground: pg}, err
	}
	return builtRenderedPlayground{composeYAML: renderedWithImages, playground: pg}, nil
}

// deployAndObserveCompose uploads Compose and waits for service state.
func (w Worker) deployAndObserveCompose(ctx context.Context, pg domain.Playground, mq domain.Marquee, project string, rendered string, services []domain.PlaygroundServiceInfo, serviceNames []string) (domain.Playground, error) {
	var err error
	pg, err = w.recordCreationStep(ctx, pg, "compose_deploy", "running", nil)
	if err != nil {
		return pg, err
	}
	if pg, err := w.requireActiveDeployment(ctx, pg); err != nil {
		return pg, err
	}
	if err := w.Runtime.DeployCompose(ctx, mq, project, pg.ID, rendered); err != nil {
		return w.failDeploymentAfterCreationStep(ctx, pg, "compose_deploy", err, serviceNames)
	}
	pg, err = w.recordCreationStep(ctx, pg, "compose_deploy", "completed", nil)
	if err != nil {
		return w.cleanupSupersededRuntime(ctx, pg, mq, project, err)
	}
	checked, err := w.requireActiveDeployment(ctx, pg)
	if err != nil {
		return w.cleanupSupersededRuntime(ctx, checked, mq, project, err)
	}
	return w.observeDeployedCompose(ctx, checked, mq, project, services, serviceNames)
}

// observeDeployedCompose waits for service state after local Compose is up.
func (w Worker) observeDeployedCompose(ctx context.Context, pg domain.Playground, mq domain.Marquee, project string, services []domain.PlaygroundServiceInfo, serviceNames []string) (domain.Playground, error) {
	var err error
	pg, err = w.recordCreationStep(ctx, pg, "runtime_observe", "running", nil)
	if err != nil {
		return w.cleanupSupersededRuntime(ctx, pg, mq, project, err)
	}
	if pg, err := w.requireActiveDeployment(ctx, pg); err != nil {
		return w.cleanupSupersededRuntime(ctx, pg, mq, project, err)
	}
	observed, err := w.observeRuntimeServices(ctx, mq, project, pg)
	if err != nil {
		failed, failErr := w.failDeploymentAfterCreationStep(ctx, pg, "runtime_observe", err, serviceNames)
		return w.cleanupSupersededRuntime(ctx, failed, mq, project, failErr)
	}
	if len(observed) > 0 {
		pg.Services = mergeServiceImages(services, observed)
		pg.ServiceURLs = mergeServiceURLState(pg.ServiceURLs, pg.Services)
	}
	pg, err = w.recordCreationStep(ctx, pg, "runtime_observe", "completed", nil)
	if err != nil {
		return w.cleanupSupersededRuntime(ctx, pg, mq, project, err)
	}
	if pg, err := w.requireActiveDeployment(ctx, pg); err != nil {
		return w.cleanupSupersededRuntime(ctx, pg, mq, project, err)
	}
	return pg, nil
}

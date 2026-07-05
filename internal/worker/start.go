package worker

import (
	"context"
	"errors"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// StartRuntimePlayground starts an existing Compose project and waits until
// routed services report the same ready state as a fresh deploy.
func (w Worker) StartRuntimePlayground(ctx context.Context, pg domain.Playground, mq *domain.Marquee) (domain.Playground, error) {
	var err error
	project, err := runtimeStartComposeProject(pg, mq)
	if err != nil {
		return pg, err
	}
	pg, serviceNames, err := w.prepareRuntimeStart(ctx, pg)
	if err != nil {
		return pg, err
	}
	if err := w.Runtime.StartCompose(ctx, *mq, project); err != nil {
		return w.failDeploymentAfterCreationStep(ctx, pg, "compose_start", err, serviceNames)
	}
	return w.observeRuntimeStart(ctx, pg, *mq, project, serviceNames)
}

// runtimeStartComposeProject validates start dependencies and returns the project.
func runtimeStartComposeProject(pg domain.Playground, mq *domain.Marquee) (string, error) {
	if mq == nil {
		return "", errors.New("marquee is required for playground start")
	}
	if pg.ComposeProject == nil || strings.TrimSpace(*pg.ComposeProject) == "" {
		return "", errors.New("playground has no compose project to start")
	}
	return strings.TrimSpace(*pg.ComposeProject), nil
}

// prepareRuntimeStart records the in-progress start state from the latest row.
func (w Worker) prepareRuntimeStart(ctx context.Context, pg domain.Playground) (domain.Playground, []string, error) {
	startStatus := pg.Status
	pg, err := w.preserveLatestRuntimeFields(ctx, pg)
	if err != nil {
		return pg, nil, err
	}
	serviceNames := playgroundServiceNames(pg.Services)
	pg.Status = domain.StatusInProgress
	pg, err = w.recordCreationStepForStart(ctx, pg, startStatus, "compose_start", "running", nil)
	if err != nil {
		return pg, nil, err
	}
	pg, err = w.requireActiveDeployment(ctx, pg)
	return pg, serviceNames, err
}

// observeRuntimeStart waits for the route-ready state after Compose starts.
func (w Worker) observeRuntimeStart(ctx context.Context, pg domain.Playground, mq domain.Marquee, project string, serviceNames []string) (domain.Playground, error) {
	pg, err := w.recordCreationStep(ctx, pg, "compose_start", "completed", nil)
	if err != nil {
		return pg, err
	}
	pg, err = w.preserveLatestRuntimeFields(ctx, pg)
	if err != nil {
		return pg, err
	}
	pg, err = w.observeDeployedCompose(ctx, pg, mq, project, pg.Services, serviceNames)
	if err != nil {
		return pg, err
	}
	pg = successfulDeploymentPlayground(pg)
	return w.recordCreationStepStrict(ctx, pg, "finalize", "completed", nil)
}

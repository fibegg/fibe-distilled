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
	if mq == nil {
		return pg, errors.New("marquee is required for playground start")
	}
	if pg.ComposeProject == nil || strings.TrimSpace(*pg.ComposeProject) == "" {
		return pg, errors.New("playground has no compose project to start")
	}
	project := strings.TrimSpace(*pg.ComposeProject)
	startStatus := pg.Status
	var err error
	pg, err = w.preserveLatestRuntimeFields(ctx, pg)
	if err != nil {
		return pg, err
	}
	serviceNames := playgroundServiceNames(pg.Services)
	pg.Status = domain.StatusInProgress
	pg, err = w.recordCreationStepForStart(ctx, pg, startStatus, "compose_start", "running", nil)
	if err != nil {
		return pg, err
	}
	if pg, err = w.requireActiveDeployment(ctx, pg); err != nil {
		return pg, err
	}
	if err := w.Runtime.StartCompose(ctx, *mq, project); err != nil {
		return w.failDeploymentAfterCreationStep(ctx, pg, "compose_start", err, serviceNames)
	}
	pg, err = w.recordCreationStep(ctx, pg, "compose_start", "completed", nil)
	if err != nil {
		return pg, err
	}
	pg, err = w.preserveLatestRuntimeFields(ctx, pg)
	if err != nil {
		return pg, err
	}
	pg, err = w.observeDeployedCompose(ctx, pg, *mq, project, pg.Services, serviceNames)
	if err != nil {
		return pg, err
	}
	pg = successfulDeploymentPlayground(pg)
	return w.recordCreationStepStrict(ctx, pg, "finalize", "completed", nil)
}

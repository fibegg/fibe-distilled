package worker

import (
	"context"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// preserveLatestRuntimeFields reloads persisted runtime fields before saving.
func (w Worker) preserveLatestRuntimeFields(ctx context.Context, pg domain.Playground) (domain.Playground, error) {
	if w.DB == nil || pg.ID == 0 {
		return pg, nil
	}
	latest, err := w.DB.GetPlayground(ctx, idString(pg.ID))
	if err != nil {
		return pg, err
	}
	return preserveLatestRuntimeFieldsFrom(pg, latest), nil
}

// preserveLatestRuntimeFieldsFrom avoids losing runtime metadata during refresh.
func preserveLatestRuntimeFieldsFrom(pg domain.Playground, latest domain.Playground) domain.Playground {
	pg.GeneratedComposeYAML = preserveString(pg.GeneratedComposeYAML, latest.GeneratedComposeYAML)
	pg.ServiceURLs = preserveSlice(pg.ServiceURLs, latest.ServiceURLs)
	pg.BuildStatuses = preserveSlice(pg.BuildStatuses, latest.BuildStatuses)
	pg.Services = preserveSlice(pg.Services, latest.Services)
	pg.CreationSteps = preserveSlice(pg.CreationSteps, latest.CreationSteps)
	pg.RootDomain = preservePointer(pg.RootDomain, latest.RootDomain)
	pg.RoutingScheme = preservePointer(pg.RoutingScheme, latest.RoutingScheme)
	pg.InternalPassword = preservePointer(pg.InternalPassword, latest.InternalPassword)
	pg.LastAppliedAt = preservePointer(pg.LastAppliedAt, latest.LastAppliedAt)
	return pg
}

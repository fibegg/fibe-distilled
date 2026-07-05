package worker

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/playguard"
)

// runtimeServiceObservation carries the result of one service inspection pass.
type runtimeServiceObservation struct {
	Playground    domain.Playground
	RefreshStatus string
	Status        domain.PlaygroundStatus
	Done          bool
}

// applyRuntimeServiceObservation saves inspected services or starts drift repair.
func (w Worker) applyRuntimeServiceObservation(ctx context.Context, pg domain.Playground, mq domain.Marquee, services []domain.PlaygroundServiceInfo, now time.Time, refreshStatus string) (runtimeServiceObservation, error) {
	result := runtimeServiceObservation{Playground: pg, RefreshStatus: refreshStatus}
	if len(services) == 0 {
		services = playguard.StoppedServiceObservations(pg.Services, pg.ServiceURLs)
	}
	if playguard.ImageDriftDetected(pg.Services, services) {
		status, err := w.repairRuntimeDrift(ctx, pg, mq, "image_drift", now, refreshStatus)
		result.Status = status
		result.Done = true
		return result, err
	}
	saved, active, err := w.saveObservedRuntimeServices(ctx, pg, services, refreshStatus)
	result.Playground = saved
	result.Status = statusFromPlayground(saved)
	if err != nil || !active {
		result.Done = true
		return result, err
	}
	result.RefreshStatus = saved.Status
	return result, nil
}

// saveObservedRuntimeServices stores runtime state observed from Docker Compose.
func (w Worker) saveObservedRuntimeServices(ctx context.Context, pg domain.Playground, services []domain.PlaygroundServiceInfo, refreshStatus string) (domain.Playground, bool, error) {
	current, active, err := w.currentRefreshPlayground(ctx, pg, refreshStatus)
	if err != nil || !active {
		return current, active, err
	}
	pg = current
	pg.Services = mergeServiceImages(pg.Services, services)
	pg.ServiceURLs = mergeServiceURLState(pg.ServiceURLs, pg.Services)
	pg = applyObservedRuntimeStatus(pg, services)
	pg, err = w.preserveLatestRuntimeFields(ctx, pg)
	if err != nil {
		return pg, true, err
	}
	saved, err := w.DB.SavePlayground(ctx, pg)
	if err != nil {
		return pg, true, err
	}
	return saved, true, nil
}

// applyObservedRuntimeStatus maps service observations onto Playground status.
func applyObservedRuntimeStatus(pg domain.Playground, services []domain.PlaygroundServiceInfo) domain.Playground {
	if anyServiceRunning(services) {
		pg.Status = domain.StatusRunning
		return clearObservedRuntimeError(pg)
	}
	if pg.Status == domain.StatusRunning || pg.Status == domain.StatusInProgress {
		pg.Status = domain.StatusStopped
	}
	return pg
}

// clearObservedRuntimeError clears prior runtime errors after services run again.
func clearObservedRuntimeError(pg domain.Playground) domain.Playground {
	pg.ErrorMessage = nil
	pg.ErrorDetails = nil
	pg.StateReason = nil
	pg.StateReasons = nil
	return pg
}

// observeRuntimeServices inspects services and waits for routed services when needed.
func (w Worker) observeRuntimeServices(ctx context.Context, mq domain.Marquee, project string, pg domain.Playground) ([]domain.PlaygroundServiceInfo, error) {
	observed, err := w.Runtime.InspectServices(ctx, mq, project)
	if err != nil || w.runtimeServicesObservationComplete(ctx, pg, observed) {
		return observed, err
	}
	return w.waitForRoutedServicesReady(ctx, mq, project, pg.ServiceURLs, observed)
}

// runtimeServicesObservationComplete reports whether service URL state is settled.
func (w Worker) runtimeServicesObservationComplete(ctx context.Context, pg domain.Playground, observed []domain.PlaygroundServiceInfo) bool {
	return len(pg.ServiceURLs) == 0 || w.routedServicesReady(ctx, observed, pg.ServiceURLs)
}

// waitForRoutedServicesReady polls Compose until routed services are usable.
func (w Worker) waitForRoutedServicesReady(ctx context.Context, mq domain.Marquee, project string, urls []domain.PlaygroundServiceURL, observed []domain.PlaygroundServiceInfo) ([]domain.PlaygroundServiceInfo, error) {
	timing := w.runtimeObservationTiming()
	timer := time.NewTimer(timing.timeout)
	defer timer.Stop()
	ticker := time.NewTicker(timing.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return observed, ctx.Err()
		case <-timer.C:
			return observed, errors.New("routed services did not become running and healthy before timeout")
		case <-ticker.C:
			next, done, err := w.routedServicesPollTick(ctx, mq, project, urls)
			observed = next
			if done || err != nil {
				return observed, err
			}
		}
	}
}

// runtimeObservationTiming contains the timeout and interval for service polling.
type runtimeObservationTiming struct {
	timeout  time.Duration
	interval time.Duration
}

// runtimeObservationTiming resolves zero worker timing fields to defaults.
func (w Worker) runtimeObservationTiming() runtimeObservationTiming {
	timing := runtimeObservationTiming{timeout: w.RuntimeObserveTimeout, interval: w.RuntimeObserveInterval}
	if timing.timeout <= 0 {
		timing.timeout = defaultRuntimeObserveTimeout
	}
	if timing.interval <= 0 {
		timing.interval = defaultRuntimeObserveInterval
	}
	return timing
}

// routedServicesPollTick returns the current service state and whether polling is done.
func (w Worker) routedServicesPollTick(ctx context.Context, mq domain.Marquee, project string, urls []domain.PlaygroundServiceURL) ([]domain.PlaygroundServiceInfo, bool, error) {
	observed, err := w.Runtime.InspectServices(ctx, mq, project)
	if err != nil {
		return observed, true, err
	}
	return observed, w.routedServicesReady(ctx, observed, urls), nil
}

// routedServicesReady checks that every public service is running and healthy.
func (w Worker) routedServicesReady(ctx context.Context, services []domain.PlaygroundServiceInfo, urls []domain.PlaygroundServiceURL) bool {
	byName := playgroundServicesByName(services)
	for _, url := range urls {
		if !w.routedServiceReady(ctx, byName, url) {
			return false
		}
	}
	return true
}

// playgroundServicesByName indexes observed services by Compose service name.
func playgroundServicesByName(services []domain.PlaygroundServiceInfo) map[string]domain.PlaygroundServiceInfo {
	byName := map[string]domain.PlaygroundServiceInfo{}
	for _, service := range services {
		byName[service.Name] = service
	}
	return byName
}

// routedServiceReady checks the running, health, and route state for one public service.
func (w Worker) routedServiceReady(ctx context.Context, services map[string]domain.PlaygroundServiceInfo, url domain.PlaygroundServiceURL) bool {
	service, ok := services[url.Name]
	if !ok || !serviceRunning(service) {
		return false
	}
	if serviceHealthExplicitlyReady(service.Health) {
		return true
	}
	if strings.TrimSpace(service.Health) != "" {
		return false
	}
	return w.routeProbeReady(ctx, url.URL)
}

// serviceRunning reports whether Compose sees a live service container.
func serviceRunning(service domain.PlaygroundServiceInfo) bool {
	return service.Running && normalizedServiceState(service.Status) == "running"
}

// serviceHealthExplicitlyReady accepts concrete ready health values from Docker.
func serviceHealthExplicitlyReady(health string) bool {
	switch normalizedServiceState(health) {
	case "running", "healthy":
		return true
	default:
		return false
	}
}

// normalizedServiceState trims and lowercases Compose status/health strings.
func normalizedServiceState(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// mergeServiceImages preserves rendered image refs when Compose inspect omits them.
func mergeServiceImages(existing []domain.PlaygroundServiceInfo, inspected []domain.PlaygroundServiceInfo) []domain.PlaygroundServiceInfo {
	imageByName := serviceImagesByName(existing)
	for i := range inspected {
		if inspected[i].Image == "" {
			inspected[i].Image = imageByName[strings.TrimSpace(inspected[i].Name)]
		}
	}
	return inspected
}

// serviceImagesByName returns trimmed image refs keyed by trimmed service name.
func serviceImagesByName(services []domain.PlaygroundServiceInfo) map[string]string {
	out := map[string]string{}
	for _, service := range services {
		name := strings.TrimSpace(service.Name)
		image := strings.TrimSpace(service.Image)
		if name != "" && image != "" {
			out[name] = image
		}
	}
	return out
}

// mergeServiceURLState copies inspected runtime state onto routed service URLs.
func mergeServiceURLState(urls []domain.PlaygroundServiceURL, services []domain.PlaygroundServiceInfo) []domain.PlaygroundServiceURL {
	byName := map[string]domain.PlaygroundServiceInfo{}
	for _, service := range services {
		byName[service.Name] = service
	}
	for i := range urls {
		service, ok := byName[urls[i].Name]
		if !ok {
			continue
		}
		urls[i].Status = service.Status
		urls[i].Health = service.Health
		urls[i].Running = new(service.Running)
		urls[i].ExitCode = service.ExitCode
	}
	return urls
}

// anyServiceRunning reports whether Compose sees at least one live service.
func anyServiceRunning(services []domain.PlaygroundServiceInfo) bool {
	for _, service := range services {
		if service.Running {
			return true
		}
	}
	return false
}

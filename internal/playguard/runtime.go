package playguard

import (
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// CanRefreshRuntime reports whether a Playground has refreshable runtime state.
func CanRefreshRuntime(pg domain.Playground, storeAvailable bool) bool {
	return pg.MarqueeID != nil && pg.ComposeProject != nil && storeAvailable
}

// ShouldCheckRuntimeArtifactDrift limits drift checks to running rendered Playgrounds.
func ShouldCheckRuntimeArtifactDrift(pg domain.Playground) bool {
	return pg.MarqueeID != nil &&
		pg.ComposeProject != nil &&
		pg.Status == domain.StatusRunning &&
		strings.TrimSpace(pg.GeneratedComposeYAML) != ""
}

// ShouldRepairRuntimeArtifacts reports whether a runtime inspect failure may be repaired.
func ShouldRepairRuntimeArtifacts(pg domain.Playground, artifactsMissing bool) bool {
	return ShouldCheckRuntimeArtifactDrift(pg) && artifactsMissing
}

// ImageDriftDetected compares observed service images with rendered image refs.
func ImageDriftDetected(expected []domain.PlaygroundServiceInfo, observed []domain.PlaygroundServiceInfo) bool {
	expectedImages := serviceImagesByName(expected)
	for _, service := range observed {
		expectedImage := expectedImages[strings.TrimSpace(service.Name)]
		observedImage := strings.TrimSpace(service.Image)
		if expectedImage != "" && observedImage != "" && observedImage != expectedImage {
			return true
		}
	}
	return false
}

// StoppedServiceObservations preserves known service identities when Compose reports no containers.
func StoppedServiceObservations(existing []domain.PlaygroundServiceInfo, urls []domain.PlaygroundServiceURL) []domain.PlaygroundServiceInfo {
	services := make([]domain.PlaygroundServiceInfo, 0, len(existing)+len(urls))
	seen := map[string]bool{}
	for _, service := range existing {
		name := strings.TrimSpace(service.Name)
		if name == "" {
			continue
		}
		seen[name] = true
		services = append(services, domain.PlaygroundServiceInfo{
			Name:    name,
			Image:   service.Image,
			Status:  domain.StatusStopped,
			Running: false,
		})
	}
	for _, serviceURL := range urls {
		name := strings.TrimSpace(serviceURL.Name)
		if name == "" || seen[name] {
			continue
		}
		services = append(services, domain.PlaygroundServiceInfo{
			Name:    name,
			Status:  domain.StatusStopped,
			Running: false,
		})
	}
	return services
}

// RuntimeRepairCooldownActive prevents every-tick repair loops.
func RuntimeRepairCooldownActive(pg domain.Playground, now time.Time) bool {
	return pg.PlayguardRepairLockUntil != nil && now.Before(*pg.PlayguardRepairLockUntil)
}

// MarkRuntimeRepairStarted writes repair cooldown and recreation markers.
func MarkRuntimeRepairStarted(pg domain.Playground, reason string, now time.Time, cooldown time.Duration) domain.Playground {
	lockUntil := now.UTC().Add(cooldown)
	pg.PlayguardRepairReason = new(reason)
	pg.PlayguardRepairLockUntil = &lockUntil
	pg.NeedsRecreation = new(true)
	return pg
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

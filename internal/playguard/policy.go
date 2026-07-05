package playguard

import (
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// NormalizeInterval resolves zero or negative intervals to the default.
func NormalizeInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 30 * time.Second
	}
	return interval
}

// ShouldSyncSources selects rows whose remote source may be updated.
func ShouldSyncSources(pg domain.Playground) bool {
	return pg.MarqueeID != nil &&
		pg.ComposeProject != nil &&
		(pg.Status == domain.StatusRunning ||
			pg.Status == domain.StatusHasChanges ||
			(pg.Status == domain.StatusError && SourceSyncStateReason(pg)))
}

// ShouldRefreshRuntime selects rows with inspectable runtime state.
func ShouldRefreshRuntime(pg domain.Playground) bool {
	if SourceSyncStateReason(pg) {
		return false
	}
	return pg.MarqueeID != nil &&
		pg.ComposeProject != nil &&
		(pg.Status == domain.StatusRunning || pg.Status == domain.StatusError || pg.Status == domain.StatusStopped)
}

// SourceSyncStateReason reports source-sync-owned lifecycle diagnostics.
func SourceSyncStateReason(pg domain.Playground) bool {
	return pg.StateReason != nil && strings.HasPrefix(*pg.StateReason, "source_sync_")
}

// MatchesRemoteProject reports whether a current DB row still represents a remote tree.
func MatchesRemoteProject(pg domain.Playground, marqueeID int64, project string) bool {
	return pg.MarqueeID != nil &&
		*pg.MarqueeID == marqueeID &&
		pg.ComposeProject != nil &&
		strings.TrimSpace(*pg.ComposeProject) == strings.TrimSpace(project)
}

// CurrentRemoteProjects returns remote projects still represented by SQLite rows.
func CurrentRemoteProjects(playgrounds []domain.Playground, marqueeID int64) map[string]bool {
	current := make(map[string]bool, len(playgrounds))
	for _, pg := range playgrounds {
		if !MatchesRemoteProject(pg, marqueeID, playgroundProject(pg)) {
			continue
		}
		current[strings.TrimSpace(*pg.ComposeProject)] = true
	}
	return current
}

// StaleRemoteProjects returns remote projects absent from the current SQLite snapshot.
func StaleRemoteProjects(remoteProjects []string, playgrounds []domain.Playground, marqueeID int64) []string {
	current := CurrentRemoteProjects(playgrounds, marqueeID)
	stale := make([]string, 0, len(remoteProjects))
	for _, project := range remoteProjects {
		project = strings.TrimSpace(project)
		if project == "" || current[project] {
			continue
		}
		stale = append(stale, project)
	}
	return stale
}

// playgroundProject returns the row's compose project text.
func playgroundProject(pg domain.Playground) string {
	if pg.ComposeProject == nil {
		return ""
	}
	return strings.TrimSpace(*pg.ComposeProject)
}

// ExpirableStatus reports statuses that expiration may act on.
func ExpirableStatus(status string) bool {
	return status == domain.StatusRunning || status == domain.StatusHasChanges
}

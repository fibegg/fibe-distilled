package playguard

import (
	"testing"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

func TestPlaygroundSelectionPolicies(t *testing.T) {
	marqueeID := int64(7)
	project := "demo--1"
	sourceReason := "source_sync_dirty_work"
	pg := domain.Playground{MarqueeID: &marqueeID, ComposeProject: &project, Status: domain.StatusRunning}

	if !ShouldSyncSources(pg) || !ShouldRefreshRuntime(pg) {
		t.Fatalf("running playground should sync and refresh")
	}
	pg.Status = domain.StatusError
	pg.StateReason = &sourceReason
	if !ShouldSyncSources(pg) || ShouldRefreshRuntime(pg) {
		t.Fatalf("source-sync error should retry sync and skip runtime refresh")
	}
	pg.StateReason = nil
	if !ShouldRefreshRuntime(pg) {
		t.Fatalf("non-source-sync error should refresh runtime")
	}
}

func TestRemoteProjectPolicies(t *testing.T) {
	marqueeID := int64(7)
	project := " demo--1 "
	pg := domain.Playground{MarqueeID: &marqueeID, ComposeProject: &project}

	if !MatchesRemoteProject(pg, marqueeID, "demo--1") {
		t.Fatalf("expected trimmed project match")
	}
	if MatchesRemoteProject(pg, 8, "demo--1") {
		t.Fatalf("different marquee must not match")
	}
	stale := StaleRemoteProjects([]string{"demo--1", "old--1", " "}, []domain.Playground{pg}, marqueeID)
	if len(stale) != 1 || stale[0] != "old--1" {
		t.Fatalf("unexpected stale project selection: %#v", stale)
	}
}

func TestRuntimeRefreshPolicies(t *testing.T) {
	marqueeID := int64(7)
	project := "demo--1"
	pg := domain.Playground{
		Status:               domain.StatusRunning,
		MarqueeID:            &marqueeID,
		ComposeProject:       &project,
		GeneratedComposeYAML: "services:\n  web:\n    image: nginx\n",
		Services:             []domain.PlaygroundServiceInfo{{Name: "web", Image: "nginx:old"}},
	}

	if !CanRefreshRuntime(pg, true) || !ShouldCheckRuntimeArtifactDrift(pg) {
		t.Fatalf("running rendered playground should be refreshable and drift-checkable")
	}
	if !ShouldRepairRuntimeArtifacts(pg, true) || ShouldRepairRuntimeArtifacts(pg, false) {
		t.Fatalf("runtime artifact repair should require a missing-artifact signal")
	}
	if !ImageDriftDetected(pg.Services, []domain.PlaygroundServiceInfo{{Name: "web", Image: "nginx:new"}}) {
		t.Fatalf("expected image drift")
	}
	stopped := StoppedServiceObservations(pg.Services, []domain.PlaygroundServiceURL{{Name: "api"}})
	if len(stopped) != 2 || stopped[0].Status != domain.StatusStopped || stopped[1].Name != "api" {
		t.Fatalf("unexpected stopped service observations: %#v", stopped)
	}

	now := time.Now().UTC()
	marked := MarkRuntimeRepairStarted(pg, "artifact_drift", now, time.Minute)
	if marked.PlayguardRepairReason == nil || *marked.PlayguardRepairReason != "artifact_drift" ||
		marked.PlayguardRepairLockUntil == nil || !RuntimeRepairCooldownActive(marked, now.Add(30*time.Second)) ||
		marked.NeedsRecreation == nil || !*marked.NeedsRecreation {
		t.Fatalf("unexpected runtime repair markers: %#v", marked)
	}
}

func TestNormalizeIntervalAndExpirationStatus(t *testing.T) {
	if NormalizeInterval(0) != 30*time.Second || NormalizeInterval(-time.Second) != 30*time.Second {
		t.Fatalf("zero and negative intervals should use default")
	}
	if NormalizeInterval(time.Minute) != time.Minute {
		t.Fatalf("positive interval should be preserved")
	}
	if !ExpirableStatus(domain.StatusRunning) || !ExpirableStatus(domain.StatusHasChanges) || ExpirableStatus(domain.StatusError) {
		t.Fatalf("unexpected expirable status policy")
	}
}

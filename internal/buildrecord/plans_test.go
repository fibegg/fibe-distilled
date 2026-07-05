package buildrecord

import (
	"strings"
	"testing"
	"time"

	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

func TestPlanCreatesPendingRecordAndRuntimeRequest(t *testing.T) {
	project := "demo--1"
	platform := "linux/amd64"
	pg := domain.Playground{ID: 42, ComposeProject: &project}
	marquee := domain.Marquee{BuildPlatform: &platform}
	summary := service.Summary{
		Name:        "web",
		Build:       true,
		RepoURL:     "https://github.com/acme/demo.git",
		Branch:      "feature/demo",
		Dockerfile:  "Dockerfile.web",
		BuildTarget: "prod",
		BuildArgs:   " NODE_VERSION = 22, =skip,APP_ENV=test ",
	}

	plan, err := NewPlan(pg, marquee, summary)
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	if plan.Project != project || plan.PlaygroundID != pg.ID || plan.ServiceName != "web" || plan.Branch != "feature/demo" {
		t.Fatalf("unexpected plan identity: %#v", plan)
	}
	if !strings.Contains(plan.SourcePath, "/opt/fibe/playgrounds/demo--1/props/acme-demo/feature-demo") {
		t.Fatalf("unexpected source path: %s", plan.SourcePath)
	}
	if plan.Identity.DockerfilePath != "Dockerfile.web" || plan.Identity.BuildTarget != "prod" || plan.Identity.BuildIdentityDigest == "" {
		t.Fatalf("unexpected build identity: %#v", plan.Identity)
	}

	propID := int64(7)
	plan = plan.WithPropID(propID)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	record := NewPendingRecord(plan, now)
	if record.PropID == nil || *record.PropID != propID || record.Status != domain.BuildStatusPending {
		t.Fatalf("unexpected pending record: %#v", record)
	}
	if record.BuildCacheKey != plan.Identity.BuildIdentityDigest || record.StartedAt == nil || !record.StartedAt.Equal(now) {
		t.Fatalf("pending record did not preserve identity/time: %#v", record)
	}

	record.CreatedAt = now
	record, err = AttachCommit(plan, record, "abcdef1234567890")
	if err != nil {
		t.Fatalf("attach commit: %v", err)
	}
	req, err := RuntimeRequest(plan, record)
	if err != nil {
		t.Fatalf("runtime request: %v", err)
	}
	if req.Project != project || req.PlaygroundID != pg.ID || req.ServiceName != "web" || req.CommitSHA != "abcdef1234567890" {
		t.Fatalf("unexpected runtime request identity: %#v", req)
	}
	if req.Dockerfile.String() != "Dockerfile.web" || req.Target != "prod" || req.Platform != domain.BuildPlatform(platform) {
		t.Fatalf("unexpected runtime build options: %#v", req)
	}
	if len(req.BuildArgs) != 2 || req.BuildArgs[0] != "NODE_VERSION=22" || req.BuildArgs[1] != "APP_ENV=test" {
		t.Fatalf("unexpected normalized build args: %#v", req.BuildArgs)
	}
}

func TestNeedsRemoteBuildSkipsLiveSourceMountEvenWithBuildBlock(t *testing.T) {
	summary := service.Summary{
		Name:        "web",
		Build:       true,
		RepoURL:     "https://github.com/acme/demo.git",
		SourceMount: "/app",
		Production:  false,
	}
	if NeedsRemoteBuild(summary) {
		t.Fatal("live source-mounted service should not create BuildRecords even when Compose keeps build")
	}
	summary.Production = true
	if !NeedsRemoteBuild(summary) {
		t.Fatal("production source-backed service should create BuildRecords")
	}
	summary.Production = false
	summary.SourceMount = ""
	if !NeedsRemoteBuild(summary) {
		t.Fatal("build-backed service without live source mount should create BuildRecords")
	}
}

func TestStatusFromRecordProjectsBuildSnapshot(t *testing.T) {
	completed := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	message := "build failed"
	record := domain.BuildRecord{
		ID:           9,
		ServiceName:  "web",
		Status:       domain.BuildStatusFailed,
		CommitSHA:    "abcdef1234567890",
		ImageRef:     "fibe-distilled/demo/web:abcdef1234567890",
		ErrorMessage: &message,
		CompletedAt:  &completed,
		CreatedAt:    completed.Add(-time.Minute),
	}

	status := StatusFromRecord("web", "main", record)
	if status.ServiceName != "web" || status.Branch != "main" || status.Latest == nil || status.Active == nil {
		t.Fatalf("unexpected status shell: %#v", status)
	}
	if status.Latest.ID != record.ID || status.Latest.ShortCommitSHA != "abcdef123456" || status.Latest.ErrorMessage != message {
		t.Fatalf("unexpected status snapshot: %#v", status.Latest)
	}
}

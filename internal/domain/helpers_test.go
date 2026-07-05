package domain

import (
	"testing"
	"time"
)

func TestFirstDomainFromInput(t *testing.T) {
	if FirstDomainFromInput(nil) != "" {
		t.Fatalf("nil input should return blank")
	}
	input := " \r\n first.example.test,second.example.test"
	if got := FirstDomainFromInput(&input); got != "first.example.test" {
		t.Fatalf("first domain = %q", got)
	}
}

func TestDockerBuildArgs(t *testing.T) {
	parts, ok := ParseDockerBuildArgs(" NODE_VERSION=22, APP_ENV=test,, USE_CACHE, K3 = v3, =skip ")
	if !ok || len(parts) != 4 || parts[0] != "NODE_VERSION=22" || parts[1] != "APP_ENV=test" || parts[2] != "USE_CACHE" || parts[3] != "K3=v3" {
		t.Fatalf("parse build args = %#v, %v", parts, ok)
	}
	for _, value := range []string{"NODE_VERSION=22", "APP_ENV=", "USE_CACHE", "_TOKEN=value=with=equals"} {
		if _, ok := NormalizeDockerBuildArg(value); !ok {
			t.Fatalf("expected valid build arg %q", value)
		}
	}
	if parts, ok := ParseDockerBuildArgs("BAD-KEY=value"); ok {
		t.Fatalf("expected invalid build args, got %#v", parts)
	}
	for _, value := range []string{"", " =value", "BAD KEY=value", "BAD-KEY=value", "1BAD=value"} {
		if normalized, ok := NormalizeDockerBuildArg(value); ok {
			t.Fatalf("expected invalid build arg %q, got %q", value, normalized)
		}
	}
	if normalized, ok := NormalizeDockerBuildArg(" K = v "); !ok || normalized != "K=v" {
		t.Fatalf("normalize build arg = %q, %v", normalized, ok)
	}
	if normalized, ok := NormalizeDockerBuildArg("=skip"); ok {
		t.Fatalf("expected invalid build arg, got %q", normalized)
	}
}

func TestPlaygroundStatusSnapshotSummarizesCreationSteps(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.FixedZone("test", 3600))
	needsRecreation := true
	pg := Playground{
		ID:              42,
		Name:            "demo",
		Status:          StatusRunning,
		NeedsRecreation: &needsRecreation,
		CreationSteps: []PlaygroundCreationStep{
			{Name: "compose_render", Label: "Render Compose", Status: "completed"},
			{Name: "host_prerequisites", Label: "Check host", Status: "error"},
			{Name: "finalize", Label: "Finalize", Status: "completed"},
		},
	}

	status := pg.StatusSnapshot(now)
	if status.ID != pg.ID || status.Name != pg.Name || status.Status != StatusRunning {
		t.Fatalf("snapshot identity = %#v", status)
	}
	if status.CreationStep != "completed" || status.CreationStepLabel != "Completed" {
		t.Fatalf("unexpected finalized creation step: %#v", status)
	}
	if status.ErrorStep != "host_prerequisites" || status.ErrorStepLabel != "Check host" {
		t.Fatalf("unexpected error step: %#v", status)
	}
	if !status.NeedsRecreation {
		t.Fatalf("expected needs_recreation to be true: %#v", status)
	}
	if !status.UpdatedAt.Equal(now.UTC()) || status.UpdatedAt.Location() != time.UTC {
		t.Fatalf("expected UTC status timestamp, got %s", status.UpdatedAt)
	}
}

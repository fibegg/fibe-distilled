package playspec

import (
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/composetest"
)

func TestApplyPlayspecServicesAffectsCompose(t *testing.T) {
	patched, err := ApplyPlayspecServices(`services:
  web:
    image: nginx:alpine
  worker:
    image: alpine
`, []any{
		map[string]any{
			"name":  "web",
			"type":  "static",
			"image": "nginx:latest",
			"exposure": map[string]any{
				"enabled":    true,
				"port":       float64(8080),
				"subdomain":  "app",
				"visibility": "external",
				"path_rule":  "PathPrefix(`/`)",
			},
		},
		map[string]any{
			"name": "worker",
			"type": "static",
		},
	})
	if err != nil {
		t.Fatalf("apply playspec services: %v", err)
	}
	for _, want := range []string{
		"image: nginx:latest",
		"fibe.gg/port: \"8080\"",
		"fibe.gg/subdomain: app",
		"fibe.gg/visibility: external",
		"fibe.gg/path_rule: PathPrefix(`/`)",
	} {
		if !strings.Contains(patched, want) {
			t.Fatalf("expected %q in patched compose:\n%s", want, patched)
		}
	}
}

func TestApplyPlayspecServicesRejectsJobWatchMetadata(t *testing.T) {
	_, err := ApplyPlayspecServices(`services:
  worker:
    image: alpine
`, []any{map[string]any{"name": "worker", "job_watch": true}})
	if err == nil || !strings.Contains(err.Error(), "job_watch is not implemented") {
		t.Fatalf("expected job_watch rejection, got %v", err)
	}
}

func TestApplyPlayspecServicesRejectsIgnoredMetadata(t *testing.T) {
	base := `services:
  api:
    image: node:22
`
	for _, tt := range []struct {
		name    string
		service map[string]any
		want    string
	}{
		{name: "blank repo", service: map[string]any{"name": "api", "repo_url": " "}, want: "services[0].repo_url"},
		{name: "numeric image", service: map[string]any{"name": "api", "image": 123}, want: "services[0].image"},
		{name: "empty exposure", service: map[string]any{"name": "api", "exposure": map[string]any{}}, want: "services[0].exposure"},
		{name: "unknown field", service: map[string]any{"name": "api", "not_real": "value"}, want: "services[0].not_real"},
		{name: "malformed production", service: map[string]any{"name": "api", "production": "maybe"}, want: "services[0].production"},
		{name: "prop classifier", service: map[string]any{"name": "api", "prop_id": 12}, want: "services[0].prop_id"},
		{name: "workdir classifier", service: map[string]any{"name": "api", "workdir": "/app"}, want: "services[0].workdir"},
		{name: "workflow classifier", service: map[string]any{"name": "api", "workflow": "build"}, want: "services[0].workflow"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyPlayspecServices(base, []any{tt.service})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestApplyPlayspecServicesRejectsCoercedServiceNames(t *testing.T) {
	_, err := ApplyPlayspecServices(`services:
  "123":
    image: node:22
`, []any{map[string]any{"name": 123, "image": "nginx:latest"}})
	if err == nil || !strings.Contains(err.Error(), "services[0].name must be a string") {
		t.Fatalf("expected numeric service name rejection, got %v", err)
	}
}

func TestApplyPlayspecServicesRequiresMutableComposeServices(t *testing.T) {
	for _, tt := range []struct {
		name string
		base string
		want string
	}{
		{name: "missing services", base: "volumes:\n  data: {}\n", want: "services metadata require compose services"},
		{name: "null services", base: "services: null\n", want: "services metadata require compose services"},
		{name: "null service body", base: "services:\n  web: null\n", want: "compose service \"web\" must be a mapping"},
		{name: "top-level null", base: "null\n", want: "compose yaml must be a mapping"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyPlayspecServices(tt.base, []any{map[string]any{"name": "web", "type": "static"}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestApplyPlayspecServicesAppliesDynamicMetadata(t *testing.T) {
	patched, err := ApplyPlayspecServices(`services:
  api:
    image: node:22
`, []any{
		map[string]any{
			"name":            "api",
			"type":            "dynamic",
			"repo_url":        "https://github.com/acme/api.git",
			"dockerfile_path": "deploy/Dockerfile",
			"start_command":   "npm ci && npm test",
			"build_target":    "runner",
			"build_args":      " NODE_VERSION = 22, =skip,APP_ENV=test ",
			"production":      true,
		},
	})
	if err != nil {
		t.Fatalf("apply playspec services: %v", err)
	}
	service := composetest.RenderedService(t, patched, "api")
	labels := service["labels"].(map[string]any)
	for key, want := range map[string]string{
		"fibe.gg/repo_url":      "https://github.com/acme/api.git",
		"fibe.gg/dockerfile":    "deploy/Dockerfile",
		"fibe.gg/start_command": "npm ci && npm test",
		"fibe.gg/build_target":  "runner",
		"fibe.gg/build_args":    "NODE_VERSION=22,APP_ENV=test",
		"fibe.gg/production":    "true",
	} {
		if labels[key] != want {
			t.Fatalf("label %s = %#v, want %q\n%s", key, labels[key], want, patched)
		}
	}
	values, ok := service["command"].([]any)
	if !ok || len(values) != 3 || values[0] != "sh" || values[1] != "-c" || values[2] != "npm ci && npm test" {
		t.Fatalf("expected start_command to also update service command, got %#v\n%s", service["command"], patched)
	}
}

func TestApplyPlayspecServicesExposureDefaultsAndDisabled(t *testing.T) {
	patched, err := ApplyPlayspecServices(`services:
  web:
    image: nginx:alpine
  db:
    image: postgres:16
    labels:
      fibe.gg/port: "99999"
      fibe.gg/subdomain: stale-db
      fibe.gg/visibility: external
`, []any{
		map[string]any{
			"name": "web",
			"type": "static",
			"exposure": map[string]any{
				"enabled":    true,
				"subdomain":  "web",
				"visibility": "external",
			},
		},
		map[string]any{
			"name": "db",
			"type": "static",
			"exposure": map[string]any{
				"enabled": "false",
				"port":    99999,
			},
		},
	})
	if err != nil {
		t.Fatalf("apply playspec services: %v", err)
	}
	for _, want := range []string{
		"fibe.gg/port: \"80\"",
		"fibe.gg/subdomain: web",
		"fibe.gg/visibility: external",
	} {
		if !strings.Contains(patched, want) {
			t.Fatalf("expected %q in patched compose:\n%s", want, patched)
		}
	}
	for _, stale := range []string{"99999", "stale-db"} {
		if strings.Contains(patched, stale) {
			t.Fatalf("disabled exposure should remove stale routing label %q:\n%s", stale, patched)
		}
	}
}

func TestApplyPlayspecServicesRejectsIgnoredServiceDefinitions(t *testing.T) {
	base := "services:\n  web:\n    image: nginx\n"
	for _, tt := range []struct {
		name     string
		services []any
		want     string
	}{
		{name: "missing name", services: []any{map[string]any{"type": "static"}}, want: "name is required"},
		{name: "non-object", services: []any{"web"}, want: "must be an object"},
		{name: "unknown service", services: []any{map[string]any{"name": "missing", "type": "static"}}, want: "is not present in compose"},
		{name: "duplicate", services: []any{map[string]any{"name": "web", "type": "static"}, map[string]any{"name": "web", "type": "static"}}, want: "duplicates"},
		{name: "invalid type", services: []any{map[string]any{"name": "web", "type": "invalid"}}, want: "type must be static or dynamic"},
		{name: "blank type", services: []any{map[string]any{"name": "web", "type": " "}}, want: "type is required when present"},
		{name: "null type", services: []any{map[string]any{"name": "web", "type": nil}}, want: "type must be a string"},
		{name: "numeric type", services: []any{map[string]any{"name": "web", "type": 123}}, want: "type must be a string"},
		{name: "unsafe dockerfile path", services: []any{map[string]any{"name": "web", "type": "dynamic", "dockerfile_path": "../Dockerfile"}}, want: "dockerfile_path"},
		{name: "malformed build args", services: []any{map[string]any{"name": "web", "type": "dynamic", "build_args": "BAD-KEY=value"}}, want: "build_args"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyPlayspecServices(base, tt.services)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

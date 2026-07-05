package playground

import (
	"strings"
	"testing"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	"github.com/fibegg/fibe-distilled/internal/composetest"
)

func TestApplyOverridesNormalizesStartCommand(t *testing.T) {
	patched, err := ApplyOverrides(`services:
  web:
    image: node:22
`, nil, map[string]any{
		"web": map[string]any{"start_command": "npm install && npm run dev"},
	})
	if err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	composetest.AssertRenderedCommand(t, patched, "web", []string{"sh", "-c", "npm install && npm run dev"})

	patched, err = ApplyOverrides(`services:
  web:
    image: node:22
`, nil, map[string]any{
		"web": map[string]any{"start_command": []any{"node", "server.js", "--port", "3000"}},
	})
	if err != nil {
		t.Fatalf("apply overrides with array: %v", err)
	}
	composetest.AssertRenderedCommand(t, patched, "web", []string{"node", "server.js", "--port", "3000"})
	if strings.Contains(patched, "fibe.gg/start_command") {
		t.Fatalf("exec-form start_command override should not be converted to a label:\n%s", patched)
	}
}

func TestApplyOverridesDoesNotCreateEmptyEnvironmentForNonEnvOverrides(t *testing.T) {
	patched, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
`, nil, map[string]any{
		"web": map[string]any{"subdomain": "demo", "image": "nginx:latest"},
	})
	if err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	web := composetest.RenderedService(t, patched, "web")
	if _, ok := web["environment"]; ok {
		t.Fatalf("non-env service override should not create an empty environment block: %#v\n%s", web["environment"], patched)
	}
}

func TestApplyOverridesRejectsUnknownServices(t *testing.T) {
	_, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
`, nil, map[string]any{
		"api": map[string]any{"subdomain": "api"},
	})
	if err == nil || !strings.Contains(err.Error(), "services.api") || !strings.Contains(err.Error(), "not present in compose") {
		t.Fatalf("expected unknown service override error, got %v", err)
	}

	_, err = ApplyOverrides(`services:
  web:
    image: nginx:alpine
`, nil, map[string]any{"web": "not-an-object"})
	if err == nil || !strings.Contains(err.Error(), "malformed override objects") {
		t.Fatalf("expected malformed service override error, got %v", err)
	}

	_, err = ApplyOverrides(`services:
  web: nginx:alpine
`, nil, map[string]any{"web": map[string]any{"subdomain": "web"}})
	if err == nil || !strings.Contains(err.Error(), "must be a mapping") {
		t.Fatalf("expected malformed compose service target error, got %v", err)
	}
}

func TestOverrideHelpersRejectMalformedServiceDefinitions(t *testing.T) {
	_, err := ApplyOverrides(`services:
  web: null
`, map[string]string{"FIBE_TEST": "1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "compose service \"web\" must be a mapping") {
		t.Fatalf("expected malformed service definition error, got %v", err)
	}
}

func TestApplyOverridesRejectsNoOpOverrideShapes(t *testing.T) {
	base := `services:
  web:
    image: nginx:alpine
`
	for _, tt := range []struct {
		name      string
		overrides map[string]any
		want      string
	}{
		{
			name:      "global unsupported field",
			overrides: map[string]any{"_global": map[string]any{"image": "nginx"}},
			want:      "services._global.image",
		},
		{
			name:      "global override must not be empty",
			overrides: map[string]any{"_global": map[string]any{}},
			want:      "services._global",
		},
		{
			name:      "global env vars must be object",
			overrides: map[string]any{"_global": map[string]any{"env_vars": "BAD=1"}},
			want:      "services._global.env_vars",
		},
		{
			name:      "global env vars must not be empty",
			overrides: map[string]any{"_global": map[string]any{"env_vars": map[string]any{}}},
			want:      "services._global.env_vars",
		},
		{
			name:      "global env vars reject blank keys",
			overrides: map[string]any{"_global": map[string]any{"env_vars": map[string]any{" ": "value"}}},
			want:      "services._global.env_vars",
		},
		{
			name:      "global env vars reject null values",
			overrides: map[string]any{"_global": map[string]any{"env_vars": map[string]any{"PORT": nil}}},
			want:      "services._global.env_vars",
		},
		{
			name:      "global env vars reject object values",
			overrides: map[string]any{"_global": map[string]any{"env_vars": map[string]any{"PORT": map[string]any{"value": 8080}}}},
			want:      "services._global.env_vars",
		},
		{
			name:      "service override must not be empty",
			overrides: map[string]any{"web": map[string]any{}},
			want:      "services.web",
		},
		{
			name:      "service env vars must be object",
			overrides: map[string]any{"web": map[string]any{"env_vars": "BAD=1"}},
			want:      "services.web.env_vars",
		},
		{
			name:      "service env vars must not be empty",
			overrides: map[string]any{"web": map[string]any{"env_vars": map[string]any{}}},
			want:      "services.web.env_vars",
		},
		{
			name:      "service env vars reject blank keys",
			overrides: map[string]any{"web": map[string]any{"env_vars": map[string]any{" ": "value"}}},
			want:      "services.web.env_vars",
		},
		{
			name:      "service env vars reject null values",
			overrides: map[string]any{"web": map[string]any{"env_vars": map[string]any{"PORT": nil}}},
			want:      "services.web.env_vars",
		},
		{
			name:      "service env vars reject list values",
			overrides: map[string]any{"web": map[string]any{"env_vars": map[string]any{"PORT": []any{8080}}}},
			want:      "services.web.env_vars",
		},
		{
			name:      "service exposure must be object",
			overrides: map[string]any{"web": map[string]any{"exposure": "external"}},
			want:      "services.web.exposure",
		},
		{
			name:      "service exposure enabled must be strict bool",
			overrides: map[string]any{"web": map[string]any{"exposure": map[string]any{"enabled": "maybe"}}},
			want:      "services.web.exposure.enabled",
		},
		{
			name:      "service scalar overrides must be nonblank",
			overrides: map[string]any{"web": map[string]any{"subdomain": "   "}},
			want:      "services.web.subdomain",
		},
		{
			name:      "service string overrides must be strings",
			overrides: map[string]any{"web": map[string]any{"image": 123}},
			want:      "services.web.image",
		},
		{
			name:      "dockerfile path rejects parent traversal",
			overrides: map[string]any{"web": map[string]any{"dockerfile_path": "../Dockerfile"}},
			want:      "services.web.dockerfile_path",
		},
		{
			name:      "dockerfile path rejects nested parent traversal",
			overrides: map[string]any{"web": map[string]any{"dockerfile_path": "deploy/../Dockerfile"}},
			want:      "services.web.dockerfile_path",
		},
		{
			name:      "env file path is unsupported",
			overrides: map[string]any{"web": map[string]any{"env_file_path": "config/app.env"}},
			want:      "services.web.env_file_path",
		},
		{
			name:      "build args reject blank key",
			overrides: map[string]any{"web": map[string]any{"build_args": " =bad"}},
			want:      "services.web.build_args",
		},
		{
			name:      "build args reject malformed key",
			overrides: map[string]any{"web": map[string]any{"build_args": "BAD-KEY=value"}},
			want:      "services.web.build_args",
		},
		{
			name:      "start command scalar must be string",
			overrides: map[string]any{"web": map[string]any{"start_command": 123}},
			want:      "services.web.start_command",
		},
		{
			name:      "start command array entries must be strings",
			overrides: map[string]any{"web": map[string]any{"start_command": []any{"node", 123}}},
			want:      "services.web.start_command",
		},
		{
			name:      "start command array entries must be nonblank",
			overrides: map[string]any{"web": map[string]any{"start_command": []any{"node", ""}}},
			want:      "services.web.start_command",
		},
		{
			name:      "start command array entries must not be null",
			overrides: map[string]any{"web": map[string]any{"start_command": []any{nil}}},
			want:      "services.web.start_command",
		},
		{
			name:      "service exposure port must be a port",
			overrides: map[string]any{"web": map[string]any{"exposure": map[string]any{"enabled": true, "port": "80.5"}}},
			want:      "services.web.exposure.port",
		},
		{
			name:      "service git config only selects existing branch",
			overrides: map[string]any{"web": map[string]any{"git_config": map[string]any{"create_branch": false}}},
			want:      "services.web.git_config.create_branch",
		},
		{
			name:      "service git config branch rejects git option",
			overrides: map[string]any{"web": map[string]any{"git_config": map[string]any{"branch_name": "--detach"}}},
			want:      "services.web.git_config.branch_name",
		},
		{
			name:      "service git config branch rejects invalid ref",
			overrides: map[string]any{"web": map[string]any{"git_config": map[string]any{"branch_name": "bad..name"}}},
			want:      "services.web.git_config.branch_name",
		},
		{
			name:      "port mapping array item must be object",
			overrides: map[string]any{"web": map[string]any{"port_mappings": []any{"80:8080"}}},
			want:      "services.web.port_mappings.0",
		},
		{
			name:      "port mapping list must not be empty",
			overrides: map[string]any{"web": map[string]any{"port_mappings": []any{}}},
			want:      "services.web.port_mappings",
		},
		{
			name:      "port mapping map must not be empty",
			overrides: map[string]any{"web": map[string]any{"port_mappings": map[string]any{}}},
			want:      "services.web.port_mappings",
		},
		{
			name:      "port mapping object needs host",
			overrides: map[string]any{"web": map[string]any{"port_mappings": []any{map[string]any{"container": "80"}}}},
			want:      "services.web.port_mappings.0.host",
		},
		{
			name:      "port mapping container must be port",
			overrides: map[string]any{"web": map[string]any{"port_mappings": []any{map[string]any{"container": "80.5", "host": "8080"}}}},
			want:      "services.web.port_mappings.0.container",
		},
		{
			name:      "port mapping host must be port",
			overrides: map[string]any{"web": map[string]any{"port_mappings": []any{map[string]any{"container": "80", "host": "abc"}}}},
			want:      "services.web.port_mappings.0.host",
		},
		{
			name:      "port mapping map host must be port",
			overrides: map[string]any{"web": map[string]any{"port_mappings": map[string]any{"80": "70000"}}},
			want:      "services.web.port_mappings.80",
		},
		{
			name:      "port mapping map key must be string",
			overrides: map[string]any{"web": map[string]any{"port_mappings": map[any]any{80: "8080"}}},
			want:      "services.web.port_mappings.<non-string-key>",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyOverrides(base, nil, tt.overrides)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestApplyOverridesAppliesPortMappings(t *testing.T) {
	patched, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
    ports:
      - 127.0.0.1:3000:80/tcp
      - 8443:443/tcp
  db:
    image: postgres:17
    ports:
      - target: 5432
        published: 15432
        protocol: udp
  cache:
    image: redis:7
    ports:
      - target: 6379
        published: 6379
        protocol: ["udp"]
`, nil, map[string]any{
		"web":   map[string]any{"port_mappings": []any{map[string]any{"container": "80", "host": "8080"}}},
		"db":    map[string]any{"port_mappings": map[string]any{"5432": "25432"}},
		"cache": map[string]any{"port_mappings": map[string]any{"6379": "26379"}},
	})
	if err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	webPorts := composetest.RenderedService(t, patched, "web")["ports"].([]any)
	if len(webPorts) != 2 || webPorts[0] != "8443:443/tcp" || webPorts[1] != "8080:80/tcp" {
		t.Fatalf("unexpected web ports: %#v\n%s", webPorts, patched)
	}
	dbPorts := composetest.RenderedService(t, patched, "db")["ports"].([]any)
	if len(dbPorts) != 1 || dbPorts[0] != "25432:5432/udp" {
		t.Fatalf("unexpected db ports: %#v\n%s", dbPorts, patched)
	}
	cachePorts := composetest.RenderedService(t, patched, "cache")["ports"].([]any)
	if len(cachePorts) != 1 || cachePorts[0] != "26379:6379" {
		t.Fatalf("malformed protocol must not be stringified, got %#v\n%s", cachePorts, patched)
	}
}

func TestApplyOverridesPreservesListEnvironment(t *testing.T) {
	patched, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
    environment:
      - EXISTING=value
      - HOST_ONLY
`, map[string]string{"GLOBAL": "global"}, map[string]any{
		"web": map[string]any{"env_vars": map[string]any{"SERVICE_PORT": 8080, "SERVICE_DEBUG": true}},
	})
	if err != nil {
		t.Fatalf("apply playground overrides: %v", err)
	}
	env, ok := composetest.RenderedService(t, patched, "web")["environment"].(map[string]any)
	if !ok {
		t.Fatalf("expected environment map after overrides:\n%s", patched)
	}
	for key, want := range map[string]any{
		"EXISTING":      "value",
		"GLOBAL":        "global",
		"SERVICE_PORT":  "8080",
		"SERVICE_DEBUG": "true",
	} {
		if env[key] != want {
			t.Fatalf("environment[%s] = %#v, want %#v\n%s", key, env[key], want, patched)
		}
	}
	if _, ok := env["HOST_ONLY"]; !ok {
		t.Fatalf("host-only environment entry should be preserved, got %#v\n%s", env, patched)
	}
}

func TestApplyOverridesRejectsMalformedExistingEnvironment(t *testing.T) {
	cases := []struct {
		name      string
		compose   string
		globalEnv map[string]string
		services  map[string]any
		want      string
	}{
		{
			name: "global env rejects map-form non-string key",
			compose: `services:
  web:
    image: nginx:alpine
    environment:
      123: bad
`,
			globalEnv: map[string]string{"GLOBAL": "1"},
			want:      `compose service "web" environment map keys must be strings`,
		},
		{
			name: "service env rejects list map non-string key",
			compose: `services:
  web:
    image: nginx:alpine
    environment:
      - 123: bad
`,
			services: map[string]any{"web": map[string]any{"env_vars": map[string]any{"SERVICE": "1"}}},
			want:     `compose service "web" environment[0] map keys must be strings`,
		},
		{
			name: "global env rejects scalar list item",
			compose: `services:
  web:
    image: nginx:alpine
    environment:
      - 123
`,
			globalEnv: map[string]string{"GLOBAL": "1"},
			want:      `compose service "web" environment[0] must be a string or string-keyed map`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ApplyOverrides(tc.compose, tc.globalEnv, tc.services)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestApplyOverridesAffectRuntimeCompose(t *testing.T) {
	patched, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app
    volumes:
      - ${FIBE_SERVICES_WEB_PATH}:/app
`, map[string]string{"GLOBAL_VAR": "global"}, map[string]any{
		"web": map[string]any{
			"env_vars":            map[string]any{"SERVICE_VAR": "service", "SERVICE_PORT": 8080, "SERVICE_DEBUG": true},
			"subdomain":           "demo",
			"exposure_port":       float64(3000),
			"exposure_visibility": "internal",
			"path_rule":           "PathPrefix(`/demo`)",
			"git_config":          map[string]any{"branch_name": "feature/demo"},
			"build_args":          " NODE_VERSION = 22, =skip,APP_ENV=test ",
		},
	})
	if err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	result, err := compose.RuntimeWithOptions(patched, "pg-one", "example.test", "https", compose.RuntimeOptions{InternalPassword: "internal-secret"})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	urls := result.ServiceURLs
	for _, want := range []string{
		"GLOBAL_VAR: global",
		"SERVICE_VAR: service",
		"SERVICE_PORT: \"8080\"",
		"SERVICE_DEBUG: \"true\"",
		"fibe.gg/branch: feature/demo",
		"fibe.gg/build_args: NODE_VERSION=22,APP_ENV=test",
		"fibe.gg/port: \"3000\"",
		"fibe.gg/visibility: internal",
		"traefik.http.routers.pg-one-web-secure.rule: Host(`demo.example.test`) && (PathPrefix(`/demo`))",
		"/opt/fibe/playgrounds/pg-one/props/acme-demo/feature-demo:/app",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered compose:\n%s", want, rendered)
		}
	}
	if len(urls) != 1 || urls[0].URL != "https://demo.example.test" || !urls[0].AuthRequired {
		t.Fatalf("unexpected urls: %#v", urls)
	}
}

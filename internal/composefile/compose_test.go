package composefile

import (
	"maps"
	"strings"
	"testing"

	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/composetest"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

const sampleCompose = `
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      fibe.gg/visibility: external
  worker:
    image: alpine
`

func TestNormalizeLabelsNormalizesSupportedShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want map[string]string
	}{
		{
			name: "string map",
			raw:  map[string]any{" fibe.gg/repo_url ": " https://github.com/acme/api "},
			want: map[string]string{"fibe.gg/repo_url": " https://github.com/acme/api "},
		},
		{
			name: "any map",
			raw:  map[any]any{" fibe.gg/repo_url ": " https://github.com/acme/api "},
			want: map[string]string{"fibe.gg/repo_url": " https://github.com/acme/api "},
		},
		{
			name: "any map skips non-string keys",
			raw:  map[any]any{123: "ignored", "fibe.gg/repo_url": "https://github.com/acme/api"},
			want: map[string]string{"fibe.gg/repo_url": "https://github.com/acme/api"},
		},
		{
			name: "any list",
			raw:  []any{" fibe.gg/repo_url=https://github.com/acme/api ", "ignored"},
			want: map[string]string{"fibe.gg/repo_url": "https://github.com/acme/api ", "ignored": ""},
		},
		{
			name: "string list",
			raw:  []string{"fibe.gg/repo_url=https://github.com/acme/api=with-equals"},
			want: map[string]string{"fibe.gg/repo_url": "https://github.com/acme/api=with-equals"},
		},
		{
			name: "object map value preserves key without stringifying",
			raw:  map[string]any{"fibe.gg/env_file": map[string]any{"path": "config/app.env"}},
			want: map[string]string{"fibe.gg/env_file": ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := service.NormalizeLabels(tc.raw); !maps.Equal(got, tc.want) {
				t.Fatalf("NormalizeLabels(%#v) = %#v; want %#v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestAsMapRequiresStringKeys(t *testing.T) {
	got, ok := AsMap(map[any]any{" name ": "web"})
	if !ok || got["name"] != "web" {
		t.Fatalf("expected string-keyed loose map to normalize, got ok=%v map=%#v", ok, got)
	}
	got, ok = AsMap(map[any]any{123: "web"})
	if ok || got != nil {
		t.Fatalf("expected non-string loose map key rejection, got ok=%v map=%#v", ok, got)
	}
}

func TestParseRejectsMalformedServiceDefinitions(t *testing.T) {
	cases := []struct {
		name    string
		compose string
		want    string
	}{
		{
			name: "null service body",
			compose: `services:
  web: null
`,
			want: "must be a mapping",
		},
		{
			name: "list service body",
			compose: `services:
  web: []
`,
			want: "cannot unmarshal",
		},
		{
			name: "blank service name",
			compose: `services:
  "":
    image: nginx
`,
			want: "compose service name is required",
		},
		{
			name: "numeric service key",
			compose: `services:
  123:
    image: nginx
`,
			want: "compose service names must be strings",
		},
		{
			name: "list x-fibe namespace",
			compose: `x-fibe.gg: []
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg must be an object",
		},
		{
			name: "numeric x-fibe namespace key",
			compose: `x-fibe.gg:
  123: bad
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg keys must be strings",
		},
		{
			name: "scalar x-fibe metadata",
			compose: `x-fibe.gg:
  metadata: broken
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.metadata must be an object",
		},
		{
			name: "numeric x-fibe metadata key",
			compose: `x-fibe.gg:
  metadata:
    123: bad
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.metadata keys must be strings",
		},
		{
			name: "numeric x-fibe variables key",
			compose: `x-fibe.gg:
  variables:
    123:
      name: Numeric
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.variables keys must be strings",
		},
		{
			name: "numeric x-fibe variable definition key",
			compose: `x-fibe.gg:
  variables:
    APP:
      name: App
      123: bad
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.variables.APP keys must be strings",
		},
		{
			name: "root job mode",
			compose: `x-fibe.gg:
  job_mode: true
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.job_mode is not implemented",
		},
		{
			name: "metadata schedule config",
			compose: `x-fibe.gg:
  metadata:
    schedule_config:
      enabled: true
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.metadata.schedule_config is not implemented",
		},
		{
			name: "metadata preserve ports true",
			compose: `x-fibe.gg:
  metadata:
    preserve_ports: true
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.metadata.preserve_ports is not implemented",
		},
		{
			name: "metadata description list",
			compose: `x-fibe.gg:
  metadata:
    description: ["broken"]
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.metadata.description must be a string",
		},
		{
			name: "metadata source defaults true",
			compose: `x-fibe.gg:
  metadata:
    source_defaults: true
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.metadata.source_defaults is not implemented",
		},
		{
			name: "metadata preserve ports string",
			compose: `x-fibe.gg:
  metadata:
    preserve_ports: "true"
services:
  web:
    image: nginx
`,
			want: "x-fibe.gg.metadata.preserve_ports must be a boolean",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDocument(tc.compose)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected parse error containing %q, got %v", tc.want, err)
			}
			validation := Validate(tc.compose)
			if validation.Valid || len(validation.Errors) == 0 || !strings.Contains(validation.Errors[0], tc.want) {
				t.Fatalf("expected validation error containing %q, got %#v", tc.want, validation)
			}
		})
	}
}

func TestParseAcceptsQuotedNumericTemplateVariableName(t *testing.T) {
	result := Validate(`x-fibe.gg:
  variables:
    "123":
      name: Numeric
      path: services.web.environment.VALUE
services:
  web:
    image: nginx
    environment: {}
`)
	if !result.Valid {
		t.Fatalf("expected quoted numeric template variable name to stay valid, got errors=%v", result.Errors)
	}
}

func TestParseAcceptsQuotedNumericServiceName(t *testing.T) {
	result := Validate(`services:
  "123":
    image: nginx
`)
	if !result.Valid {
		t.Fatalf("expected quoted numeric service name to stay valid, got errors=%v", result.Errors)
	}
	if len(result.Services) != 1 || result.Services[0].Name != "123" {
		t.Fatalf("unexpected services: %#v", result.Services)
	}
}

func TestValidateExtractsServicesFromMapAndListLabels(t *testing.T) {
	result := Validate(sampleCompose)
	if !result.Valid {
		t.Fatalf("expected valid compose, got errors=%v", result.Errors)
	}
	if len(result.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(result.Services))
	}
	if result.Services[0].Name != "web" || result.Services[0].Port != 80 || result.Services[0].Subdomain != "app" {
		t.Fatalf("unexpected web summary: %#v", result.Services[0])
	}
	if result.Services[1].Name != "worker" {
		t.Fatalf("unexpected worker summary: %#v", result.Services[1])
	}
}

func TestValidateDynamicServiceWithoutEnvFileMetadata(t *testing.T) {
	result := Validate(`services:
  api:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/api.git
`)
	if !result.Valid {
		t.Fatalf("expected valid compose, got errors=%v", result.Errors)
	}
	api := serviceSummaryByName(t, result.Services, "api")
	if api.Type != "dynamic" {
		t.Fatalf("expected dynamic service, got %#v", api)
	}
}

func TestValidateChecksTemplateVariableContract(t *testing.T) {
	cases := []invalidComposeCase{
		{
			name: "undeclared inline variable",
			compose: `services:
  alpha:
    image: alpine
    command: ["sh", "-c", "echo $$var__MISSING"]
`,
			want: "undeclared template variables: MISSING",
		},
		{
			name: "invalid regex without launch value",
			compose: `x-fibe.gg:
  variables:
    TOKEN:
      name: Token
      validation: "/[[/"
      path: services.alpha.environment.TOKEN
services:
  alpha:
    image: alpine
    environment: {}
`,
			want: `variable "TOKEN" validation regex is invalid`,
		},
		{
			name: "default contains template token",
			compose: `x-fibe.gg:
  variables:
    PUBLIC_URL:
      name: Public URL
      default: "https://$$var__HOST.$$root_domain"
      path: services.alpha.environment.PUBLIC_URL
services:
  alpha:
    image: alpine
    environment: {}
`,
			want: `variable "PUBLIC_URL" default must be a literal`,
		},
		{
			name: "path targets missing service",
			compose: `x-fibe.gg:
  variables:
    IMAGE:
      name: Image
      path: services.missing.image
services:
  alpha:
    image: alpine
`,
			want: "template variable paths target missing services: IMAGE:missing",
		},
	}
	assertInvalidComposeCases(t, cases)
}

func TestValidateAllowsRequiredTemplateVariableWithoutLaunchValue(t *testing.T) {
	result := Validate(`x-fibe.gg:
  variables:
    IMAGE:
      name: Image
      required: true
      path: services.alpha.image
services:
  alpha:
    image: alpine
`)
	if !result.Valid {
		t.Fatalf("expected required launch-time variable to validate, got errors=%v", result.Errors)
	}
}

func TestRuntimeInjectsTraefik(t *testing.T) {
	result, err := RuntimeWithOptions(sampleCompose, "pg-one", "example.test", "https", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	services := result.Services
	urls := result.ServiceURLs
	if len(services) != 2 {
		t.Fatalf("expected services, got %#v", services)
	}
	if len(urls) != 1 || urls[0].URL != "https://app.example.test" {
		t.Fatalf("unexpected service urls: %#v", urls)
	}
	if !strings.Contains(rendered, "traefik.http.routers.pg-one-web-secure.rule") || !strings.Contains(rendered, "traefik.http.routers.pg-one-web-http.middlewares: pg-one-web-redirect") || !strings.Contains(rendered, "traefik.docker.network: pg-one_default") {
		t.Fatalf("expected traefik labels in rendered compose:\n%s", rendered)
	}
	if !strings.Contains(rendered, "fibe-distilled.managed: \"true\"") {
		t.Fatalf("expected fibe-distilled ownership label in rendered compose:\n%s", rendered)
	}
	if strings.Contains(rendered, "8080:80") {
		t.Fatalf("runtime compose should strip raw host ports:\n%s", rendered)
	}
}

func TestRuntimeRoutesOnlyFibePortLabels(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  api:
    image: nginx:alpine
    ports: ["8080:80"]
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: api
  kafka:
    image: apache/kafka:4.1.2
    ports: ["9092:9092"]
`, "pg-one", "example.test", "https", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	services := result.Services
	urls := result.ServiceURLs
	if len(services) != 2 {
		t.Fatalf("expected service metadata for both services, got %#v", services)
	}
	if len(urls) != 1 || urls[0].Name != "api" {
		t.Fatalf("raw ports must not create HTTP service URLs, got %#v", urls)
	}
	if strings.Contains(rendered, "9092:9092") || strings.Contains(rendered, "8080:80") {
		t.Fatalf("runtime compose should strip raw host ports:\n%s", rendered)
	}
	if strings.Contains(rendered, "pg-one-kafka") || strings.Contains(rendered, "kafka.example.test") {
		t.Fatalf("service without fibe.gg/port should not get Traefik routing:\n%s", rendered)
	}
}

func TestRuntimeRejectsInvalidFibeLabels(t *testing.T) {
	assertRuntimeError(t, `services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "99999"
      fibe.gg/subdomain: app
`, "fibe.gg/port must be between 1 and 65535")
}

func TestRuntimeDisablesDockerHealthcheckOnlyForRoutedServices(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  web:
    image: nginx:alpine
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:80"]
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
  worker:
    image: alpine
    healthcheck:
      test: ["CMD", "true"]
`, "pg-one", "example.test", "https", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v\n%s", err, rendered)
	}
	services := doc["services"].(map[string]any)
	webHealth := services["web"].(map[string]any)["healthcheck"].(map[string]any)
	if webHealth["disable"] != true {
		t.Fatalf("routed service healthcheck should be disabled, got %#v\n%s", webHealth, rendered)
	}
	workerHealth := services["worker"].(map[string]any)["healthcheck"].(map[string]any)
	if workerHealth["disable"] == true || len(workerHealth["test"].([]any)) == 0 {
		t.Fatalf("internal service healthcheck should be preserved, got %#v\n%s", workerHealth, rendered)
	}
}

func TestRuntimeInjectsInternalBasicAuthMiddleware(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      fibe.gg/visibility: internal
`, "pg-one", "example.test", "https", RuntimeOptions{InternalPassword: "secret-password", PlaygroundID: 123})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	urls := result.ServiceURLs
	for _, want := range []string{
		"traefik.http.routers.pg-one-web-secure.middlewares: pg-one-web-auth",
		"traefik.http.middlewares.pg-one-web-auth.basicauth.users: playground:$$2",
		"$$10$$",
		"-123",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered compose:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "secret-password") {
		t.Fatalf("rendered compose must not contain raw internal password:\n%s", rendered)
	}
	if len(urls) != 1 || !urls[0].AuthRequired || urls[0].Visibility != "internal" {
		t.Fatalf("unexpected urls: %#v", urls)
	}
}

func TestRuntimeRejectsInternalBasicAuthWithoutPassword(t *testing.T) {
	assertRuntimeError(t, `services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      fibe.gg/visibility: internal
`, "requires a Basic Auth password")
}

func TestRuntimeRejectsNonStringTraefikMiddlewareLabel(t *testing.T) {
	_, err := RuntimeWithOptions(`services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      traefik.http.routers.pg-one-web-http.middlewares: 123
`, "pg-one", "example.test", "https", RuntimeOptions{})
	if err == nil {
		t.Fatalf("expected non-string middleware label error")
	}
	if !strings.Contains(err.Error(), `label "traefik.http.routers.pg-one-web-http.middlewares" must be a string value`) {
		t.Fatalf("expected middleware label type error, got %v", err)
	}
}

func TestRuntimePreservesCustomBasicAuthMiddleware(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      fibe.gg/visibility: internal
      traefik.http.middlewares.register-auth.basicauth.users: admin:hash
      traefik.http.routers.register-only.middlewares: register-auth
`, "pg-one", "example.test", "https", RuntimeOptions{InternalPassword: "secret-password", PlaygroundID: 123})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	if !strings.Contains(rendered, "traefik.http.middlewares.register-auth.basicauth.users: admin:hash") {
		t.Fatalf("expected custom auth to be preserved:\n%s", rendered)
	}
	if strings.Contains(rendered, "pg-one-web-auth") {
		t.Fatalf("must not inject whole-service auth when custom basic auth is present:\n%s", rendered)
	}
}

func TestRuntimeUsesServiceSpecificBasicAuthPassword(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  web:
    image: nginx:alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/subdomain: app
      fibe.gg/visibility: internal
`, "pg-one", "example.test", "https", RuntimeOptions{
		InternalPassword: "global-password",
		PlaygroundID:     123,
		ServicePasswords: map[string]string{"web": "service-password"},
	})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	if strings.Contains(rendered, "global-password") || strings.Contains(rendered, "service-password") {
		t.Fatalf("rendered compose must not contain raw auth passwords:\n%s", rendered)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v", err)
	}
	services := doc["services"].(map[string]any)
	web := services["web"].(map[string]any)
	labels := web["labels"].(map[string]any)
	users := labels["traefik.http.middlewares.pg-one-web-auth.basicauth.users"].(string)
	hash := strings.TrimPrefix(users, "playground:")
	hash = strings.TrimSuffix(hash, "-123")
	hash = strings.ReplaceAll(hash, "$$", "$")
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("service-password")); err != nil {
		t.Fatalf("auth hash should match service password: %v\nusers=%s", err, users)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("global-password")); err == nil {
		t.Fatalf("auth hash unexpectedly matched global password")
	}
}

func TestRuntimeAppliesStartCommandLabel(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  web:
    image: node:22
    labels:
      fibe.gg/start_command: npm run dev
`, "pg-one", "example.test", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	command := composetest.RenderedService(t, rendered, "web")["command"]
	if command != "npm run dev" {
		t.Fatalf("expected simple start_command as string, got %#v\n%s", command, rendered)
	}

	result, err = RuntimeWithOptions(`services:
  web:
    image: node:22
    labels:
      fibe.gg/start_command: npm ci && npm test
`, "pg-one", "example.test", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered = result.ComposeYAML
	composetest.AssertRenderedCommand(t, rendered, "web", []string{"sh", "-c", "npm ci && npm test"})
}

func TestValidateRejectsInvalidFibeLabelSemantics(t *testing.T) {
	cases := []invalidComposeCase{
		{
			name: "unknown fibe label",
			compose: `services:
  web:
    image: nginx
    labels:
      fibe.gg/not_a_real_label: "1"
`,
			want: "unknown label",
		},
		{
			name: "job watch unsupported",
			compose: `services:
  web:
    image: nginx
    labels:
      fibe.gg/job_watch: "true"
`,
			want: "outside fibe-distilled",
		},
		{
			name: "padded unsupported fibe label key",
			compose: `services:
  web:
    image: nginx
    labels:
      " fibe.gg/job_watch ": "true"
`,
			want: "outside fibe-distilled",
		},
		{
			name: "job watch unsupported key-only list label",
			compose: `services:
  web:
    image: nginx
    labels:
      - fibe.gg/job_watch
`,
			want: "outside fibe-distilled",
		},
		{
			name: "unknown key-only list label",
			compose: `services:
  web:
    image: nginx
    labels:
      - fibe.gg/not_a_real_label
`,
			want: "unknown label",
		},
		{
			name: "supported key-only list label rejected",
			compose: `services:
  web:
    image: nginx
    labels:
      - fibe.gg/production
`,
			want: "label \"fibe.gg/production\" must not be blank",
		},
		{
			name: "blank routed port label rejected",
			compose: `services:
  web:
    image: nginx
    labels:
      fibe.gg/port: ""
`,
			want: "label \"fibe.gg/port\" must not be blank",
		},
		{
			name: "blank subdomain label rejected",
			compose: `services:
  web:
    image: nginx
    labels:
      fibe.gg/subdomain: " "
`,
			want: "label \"fibe.gg/subdomain\" must not be blank",
		},
		{
			name: "non-string list label rejected",
			compose: `services:
  web:
    image: nginx
    labels:
      - fibe.gg/port: 80
`,
			want: "labels[0] must be a string label item",
		},
		{
			name: "non-string map label key rejected",
			compose: `services:
  web:
    image: nginx
    labels:
      123: "bad"
`,
			want: "label map keys must be strings",
		},
		{
			name: "blank map label key rejected",
			compose: `services:
  web:
    image: nginx
    labels:
      " ": "bad"
`,
			want: "label key must not be blank",
		},
		{
			name: "object map label value rejected",
			compose: `services:
  web:
    image: nginx
    labels:
      com.example.config:
        nested: value
`,
			want: "label \"com.example.config\" must be a string, number, or boolean value",
		},
		{
			name: "list map label value rejected",
			compose: `services:
  web:
    image: nginx
    labels:
      com.example.values:
        - one
`,
			want: "label \"com.example.values\" must be a string, number, or boolean value",
		},
		{
			name: "build without repo",
			compose: `services:
  web:
    build: .
`,
			want: "requires fibe.gg/repo_url",
		},
		{
			name: "repo shorthand rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: acme/demo
`,
			want: "cloneable Git URL",
		},
		{
			name: "github extra path rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo/extra
`,
			want: "cloneable Git URL",
		},
		{
			name: "github unsupported scheme rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: http://github.com/acme/demo.git
`,
			want: "cloneable Git URL",
		},
		{
			name: "repository query suffix rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git?tab=readme
`,
			want: "cloneable Git URL",
		},
		{
			name: "repository fragment suffix rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: ssh://git.example.test/acme/demo.git#readme
`,
			want: "cloneable Git URL",
		},
		{
			name: "url repository parent segment rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://git.example.test/acme/../demo.git
`,
			want: "cloneable Git URL",
		},
		{
			name: "url repository encoded parent segment rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://git.example.test/acme/%2e%2e/demo.git
`,
			want: "cloneable Git URL",
		},
		{
			name: "unsupported repository scheme rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: foo://git.example.test/acme/demo.git
`,
			want: "cloneable Git URL",
		},
		{
			name: "repository host without path rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: ssh://git.example.test
`,
			want: "cloneable Git URL",
		},
		{
			name: "scp repository root path rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: git@git.example.test:/
`,
			want: "cloneable Git URL",
		},
		{
			name: "scp repository parent segment rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: git@git.example.test:acme/../demo.git
`,
			want: "cloneable Git URL",
		},
		{
			name: "source mount without repo",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/source_mount: /app
`,
			want: "requires fibe.gg/repo_url",
		},
		{
			name: "source mount must be absolute",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: app
`,
			want: "absolute container path",
		},
		{
			name: "source mount rejects parent traversal",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/source_mount: /app/../secrets
`,
			want: "absolute container path",
		},
		{
			name: "dockerfile rejects parent traversal",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/dockerfile: ../Dockerfile
`,
			want: "relative repository path",
		},
		{
			name: "dockerfile rejects nested parent traversal",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/dockerfile: deploy/../Dockerfile
`,
			want: "relative repository path",
		},
		{
			name: "env file label is unsupported",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/env_file: config/app.env
`,
			want: "env-file default resolution is outside fibe-distilled",
		},
		{
			name: "compose env file is unsupported",
			compose: `services:
  web:
    image: alpine
    env_file:
      - config/app.env
`,
			want: "env-file resolution is outside fibe-distilled",
		},
		{
			name: "build args reject blank key",
			compose: `services:
  web:
    build: .
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/build_args: " =bad"
`,
			want: "comma-separated Docker build args",
		},
		{
			name: "build args reject malformed key",
			compose: `services:
  web:
    build: .
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/build_args: "BAD-KEY=value"
`,
			want: "comma-separated Docker build args",
		},
		{
			name: "branch rejects git option",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/branch: --detach
`,
			want: "valid Git branch name",
		},
		{
			name: "branch rejects invalid ref",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/demo.git
      fibe.gg/branch: bad..name
`,
			want: "valid Git branch name",
		},
		{
			name: "credentialed repo url",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://x-access-token:ghp_secret@github.com/acme/demo.git
`,
			want: "must not include credentials",
		},
		{
			name: "visibility without port",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/visibility: internal
`,
			want: "requires fibe.gg/port",
		},
		{
			name: "invalid visibility",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/visibility: private
`,
			want: "visibility",
		},
		{
			name: "path rule rejects non-path predicate name",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/path_rule: BadPath("/demo")
`,
			want: "path_rule must use Path, PathPrefix, or PathRegexp",
		},
		{
			name: "path rule rejects mixed non-path predicate",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/port: "80"
      fibe.gg/path_rule: PathPrefix("/demo") && Query("debug", "1")
`,
			want: "path_rule cannot use Query",
		},
		{
			name: "fractional port label rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/port: 80.5
`,
			want: "port must be between 1 and 65535",
		},
		{
			name: "numeric repo label rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: 123
`,
			want: "label \"fibe.gg/repo_url\" must be a string value",
		},
		{
			name: "boolean subdomain label rejected",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/subdomain: true
`,
			want: "label \"fibe.gg/subdomain\" must be a string value",
		},
		{
			name: "invalid subdomain",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/subdomain: Bad_Subdomain
`,
			want: "subdomain",
		},
		{
			name: "templated source mount parent traversal rejected",
			compose: `x-fibe.gg:
  variables:
    MOUNT:
      name: Mount
      default: app
services:
  web:
    image: alpine
    labels:
      fibe.gg/repo_url: https://github.com/acme/app
      fibe.gg/source_mount: /srv/../$$var__MOUNT
`,
			want: "source_mount",
		},
		{
			name: "healthcheck labels unsupported",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/healthcheck_interval: 5seconds
`,
			want: "outside fibe-distilled's stateless runtime scope",
		},
		{
			name: "healthcheck retries unsupported",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/healthcheck_retries: "0"
`,
			want: "outside fibe-distilled's stateless runtime scope",
		},
		{
			name: "zerodowntime unsupported",
			compose: `services:
  web:
    image: alpine
    labels:
      fibe.gg/zerodowntime: "true"
`,
			want: "outside fibe-distilled's stateless runtime scope",
		},
		{
			name: "zerodowntime unsupported with template variable",
			compose: `x-fibe.gg:
  variables:
    ZERO:
      name: Zero downtime
      default: "true"
services:
  web:
    image: alpine
    labels:
      fibe.gg/zerodowntime: $$var__ZERO
`,
			want: "outside fibe-distilled's stateless runtime scope",
		},
		{
			name: "zerodowntime unsupported even with port",
			compose: `services:
  web:
    image: alpine
    container_name: fixed-web
    labels:
      fibe.gg/port: "80"
      fibe.gg/zerodowntime: "true"
`,
			want: "outside fibe-distilled's stateless runtime scope",
		},
	}
	assertInvalidComposeCases(t, cases)
}

func TestValidateAcceptsTemplateVariablesInSupportedFibeLabels(t *testing.T) {
	result := Validate(`x-fibe.gg:
  variables:
    OWNER:
      name: Owner
      default: acme
    PORT:
      name: Port
      default: "3000"
    VISIBILITY:
      name: Visibility
      default: external
    PRODUCTION:
      name: Production
      default: "true"
    SUBDOMAIN:
      name: Subdomain
      default: app
    PATH_PREFIX:
      name: Path prefix
      default: api
    BRANCH:
      name: Branch
      default: main
    SOURCE_MOUNT:
      name: Source mount
      default: /app
    DOCKERFILE:
      name: Dockerfile
      default: Dockerfile
    BUILD_ARGS:
      name: Build args
      default: FOO=bar
services:
  web:
    image: nginx
    build: .
    labels:
      fibe.gg/repo_url: https://github.com/$$var__OWNER/repo
      fibe.gg/port: $$var__PORT
      fibe.gg/visibility: $$var__VISIBILITY
      fibe.gg/production: $$var__PRODUCTION
      fibe.gg/subdomain: $$var__SUBDOMAIN
      fibe.gg/path_rule: PathPrefix("/$$var__PATH_PREFIX")
      fibe.gg/branch: $$var__BRANCH
      fibe.gg/source_mount: $$var__SOURCE_MOUNT
      fibe.gg/dockerfile: $$var__DOCKERFILE
      fibe.gg/build_args: $$var__BUILD_ARGS
`)
	if !result.Valid {
		t.Fatalf("expected supported templated labels to validate, got errors=%v", result.Errors)
	}
}

func TestValidateAcceptsTypedPortAndBooleanLabels(t *testing.T) {
	result := Validate(`services:
  web:
    image: nginx
    labels:
      fibe.gg/port: 80
      fibe.gg/production: true
      fibe.gg/path_rule: PathPrefix("/app") || PathRegexp("/Query\\(debug\\)") || Path("/health")
`)
	if !result.Valid {
		t.Fatalf("expected valid compose, got errors=%v", result.Errors)
	}
}

func TestRuntimeRejectsUncompiledTemplateTokens(t *testing.T) {
	assertRuntimeError(t, `x-fibe.gg:
  variables:
    PORT:
      name: Port
      default: "3000"
services:
  web:
    image: nginx
    labels:
      fibe.gg/port: $$var__PORT
`, "unresolved Fibe template variables")
}

func TestRuntimeUsesFibeSourcePathSlugAndSanitizedBranch(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  test:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/my-api.git/
      fibe.gg/source_mount: /app
      fibe.gg/branch: feature/new-ui
    volumes:
      - ${FIBE_SERVICES_TEST_PATH}:/app
`, "demo--1", "", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	want := "/opt/fibe/playgrounds/demo--1/props/acme-my-api/feature-new-ui:/app"
	if !strings.Contains(rendered, want) {
		t.Fatalf("expected source path %q in rendered compose:\n%s", want, rendered)
	}
}

func TestRuntimeSourceMountReplacesTargetOnlyShortVolume(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  test:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/my-api.git
      fibe.gg/source_mount: /app
    volumes:
      - /app
      - cache:/cache
`, "demo--1", "", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	assertTextContainsAll(t, rendered, []string{
		"/opt/fibe/playgrounds/demo--1/props/acme-my-api/main:/app",
		"cache:/cache",
	})
	if strings.Contains(rendered, "- /app\n") {
		t.Fatalf("target-only source mount should be replaced, got:\n%s", rendered)
	}
}

func TestRuntimeSourceMountKeepsBuildOnRemoteCheckout(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  test:
    image: node:22
    build:
      context: .
      dockerfile: Dockerfile.dev
    labels:
      fibe.gg/repo_url: https://github.com/acme/my-api.git
      fibe.gg/source_mount: /app
      fibe.gg/dockerfile: Dockerfile.runtime
      fibe.gg/build_target: dev
      fibe.gg/build_args: NODE_VERSION=22,APP_ENV=development
      fibe.gg/production: "false"
    volumes:
      - ./:/app
      - cache:/cache
`, "demo--1", "", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	assertTextContainsAll(t, rendered, []string{
		"/opt/fibe/playgrounds/demo--1/props/acme-my-api/main:/app",
		"context: /opt/fibe/playgrounds/demo--1/props/acme-my-api/main",
		"dockerfile: Dockerfile.runtime",
		"target: dev",
		"- NODE_VERSION=22",
		"- APP_ENV=development",
		"cache:/cache",
	})
	if strings.Contains(rendered, "./:/app") {
		t.Fatalf("local source mount should be replaced, got:\n%s", rendered)
	}
}

func TestRuntimeProductionModeRemovesSourceMount(t *testing.T) {
	composeYAML := `services:
  test:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/my-api.git
      fibe.gg/source_mount: /app
      fibe.gg/production: "true"
    volumes:
      - ./:/app
      - cache:/cache
  sidecar:
    image: alpine
    volumes:
      - ${FIBE_SERVICES_TEST_PATH:-.}:/mirror
`
	validation := Validate(composeYAML)
	if !validation.Valid {
		t.Fatalf("expected valid production-mode compose, got errors=%v", validation.Errors)
	}
	productionService := serviceSummaryByName(t, validation.Services, "test")
	if !productionService.Build || !productionService.Production {
		t.Fatalf("production-mode repo service should be planned as a build, got %#v", productionService)
	}
	result, err := RuntimeWithOptions(composeYAML, "demo--1", "", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	for _, unwanted := range []string{".:/app", "./:/app", "/opt/fibe/playgrounds/demo--1/props/acme-my-api/main:/mirror"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("did not expect %q in rendered compose:\n%s", unwanted, rendered)
		}
	}
	assertTextContainsAll(t, rendered, []string{
		"cache:/cache",
		"context: /opt/fibe/playgrounds/demo--1/props/acme-my-api/main",
	})
}

func TestRuntimeProductionModeRemovesTargetOnlyShortSourceMount(t *testing.T) {
	result, err := RuntimeWithOptions(`services:
  test:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/my-api.git
      fibe.gg/source_mount: /app
      fibe.gg/production: "true"
    volumes:
      - /app
      - cache:/cache
`, "demo--1", "", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	service := composetest.RenderedService(t, rendered, "test")
	if renderedHasVolumeTarget(service, "/app") {
		t.Fatalf("production mode should remove target-only source mount:\n%s", rendered)
	}
	if !renderedHasVolumeTarget(service, "/cache") {
		t.Fatalf("production mode should preserve unrelated volumes:\n%s", rendered)
	}
	if !strings.Contains(rendered, "context: /opt/fibe/playgrounds/demo--1/props/acme-my-api/main") {
		t.Fatalf("production mode should build from remote source checkout:\n%s", rendered)
	}
}

func TestRuntimeSourceMountHandlesLongFormVolumes(t *testing.T) {
	composeYAML := `services:
  test:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/my-api.git
      fibe.gg/source_mount: /app
    volumes:
      - type: bind
        source: .
        target: /app
        read_only: true
      - type: volume
        source: cache
        target: /cache
`
	result, err := RuntimeWithOptions(composeYAML, "demo--1", "", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	sourceMount := composetest.RenderedVolumeMapAtTarget(t, composetest.RenderedService(t, rendered, "test"), "/app")
	if sourceMount["source"] != "/opt/fibe/playgrounds/demo--1/props/acme-my-api/main" {
		t.Fatalf("long-form source mount source = %#v", sourceMount)
	}
	if sourceMount["type"] != "bind" || sourceMount["read_only"] != true {
		t.Fatalf("long-form source mount should preserve bind options, got %#v", sourceMount)
	}
	if !renderedHasVolumeTarget(composetest.RenderedService(t, rendered, "test"), "/cache") {
		t.Fatalf("long-form source rewrite should preserve unrelated volumes:\n%s", rendered)
	}
}

func TestRuntimeProductionModeRemovesLongFormSourceMount(t *testing.T) {
	composeYAML := `services:
  test:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/my-api.git
      fibe.gg/source_mount: /app
      fibe.gg/production: "true"
    volumes:
      - type: bind
        source: .
        target: /app
      - type: volume
        source: cache
        target: /cache
`
	result, err := RuntimeWithOptions(composeYAML, "demo--1", "", "http", RuntimeOptions{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	rendered := result.ComposeYAML
	service := composetest.RenderedService(t, rendered, "test")
	if renderedHasVolumeTarget(service, "/app") {
		t.Fatalf("production mode should remove long-form source mount:\n%s", rendered)
	}
	if !renderedHasVolumeTarget(service, "/cache") {
		t.Fatalf("production mode should preserve unrelated long-form volumes:\n%s", rendered)
	}
	if !strings.Contains(rendered, "context: /opt/fibe/playgrounds/demo--1/props/acme-my-api/main") {
		t.Fatalf("production mode should build from remote source checkout:\n%s", rendered)
	}
}

func renderedHasVolumeTarget(service map[string]any, target string) bool {
	volumes, ok := service["volumes"].([]any)
	if !ok {
		return false
	}
	for _, item := range volumes {
		if renderedVolumeItemTarget(item) == target {
			return true
		}
	}
	return false
}

func serviceSummaryByName(t *testing.T, services []service.Summary, name string) service.Summary {
	t.Helper()
	for _, service := range services {
		if service.Name == name {
			return service
		}
	}
	t.Fatalf("service summary %q not found in %#v", name, services)
	return service.Summary{}
}

// renderedVolumeItemTarget returns the target path from a rendered Compose volume item.
func renderedVolumeItemTarget(raw any) string {
	switch value := raw.(type) {
	case string:
		parts := strings.Split(value, ":")
		if len(parts) >= 2 {
			return parts[1]
		}
		return value
	case map[string]any:
		if target, ok := value["target"].(string); ok {
			return target
		}
	}
	return ""
}

type invalidComposeCase struct {
	name    string
	compose string
	want    string
}

func assertInvalidComposeCases(t *testing.T, cases []invalidComposeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := Validate(tc.compose)
			if result.Valid {
				t.Fatalf("expected invalid compose")
			}
			if !strings.Contains(strings.Join(result.Errors, "\n"), tc.want) {
				t.Fatalf("expected error containing %q, got %#v", tc.want, result.Errors)
			}
		})
	}
}

func assertRuntimeError(t *testing.T, compose string, want string) {
	t.Helper()
	_, err := RuntimeWithOptions(compose, "pg-one", "example.test", "https", RuntimeOptions{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected runtime error containing %q, got %v", want, err)
	}
}

func assertTextContainsAll(t *testing.T, text string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in text:\n%s", want, text)
		}
	}
}

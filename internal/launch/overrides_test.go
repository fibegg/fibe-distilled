package launch

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/fibegg/fibe-distilled/internal/composetest"
	"gopkg.in/yaml.v3"
)

func TestApplyLaunchOverridesCompilesTemplateVariables(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  variables:
    TAG:
      name: Image tag
      default: "1.2.3"
    APP_NAME:
      name: App name
      required: true
      validation: "/^[a-z0-9-]+$/"
      paths:
        - services.web.environment.APP_NAME
        - services.web.labels.fibe.gg/subdomain
    REPLICAS:
      name: Replicas
      default: 2
      path: services.web.deploy.replicas
    FEATURE_ENABLED:
      name: Feature enabled
      default: true
    SECRET:
      name: Secret
      random: true
      path: services.web.environment.SECRET
services:
  web:
    image: ghcr.io/acme/app:$$var__TAG
    environment:
      APP_NAME: local
      DATABASE_URL: postgres://postgres:$$var__APP_NAME@db/app
      FEATURE_FLAG: $$var__FEATURE_ENABLED
      REPLICA_TEXT: replicas-$$var__REPLICAS
      SECRET: local-secret
    labels:
      fibe.gg/subdomain: local
`, map[string]string{"APP_NAME": "demo-app", "REPLICAS": "3"}, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v\n%s", err, rendered)
	}
	web := doc["services"].(map[string]any)["web"].(map[string]any)
	if web["image"] != "ghcr.io/acme/app:1.2.3" {
		t.Fatalf("inline default variable was not applied: %#v", web["image"])
	}

	env := web["environment"].(map[string]any)
	if env["APP_NAME"] != "demo-app" || env["DATABASE_URL"] != "postgres://postgres:demo-app@db/app" {
		t.Fatalf("path and inline variables were not applied: %#v", env)
	}
	if env["FEATURE_FLAG"] != "true" || env["REPLICA_TEXT"] != "replicas-3" {
		t.Fatalf("inline scalar variables were not applied: %#v", env)
	}
	if secret, ok := env["SECRET"].(string); !ok || len(secret) != 32 {
		t.Fatalf("random secret should be a 32-char hex string, got %#v", env["SECRET"])
	}

	deploy := web["deploy"].(map[string]any)
	if deploy["replicas"] != 3 {
		t.Fatalf("numeric path value should be coerced, got %#v", deploy["replicas"])
	}

	labels := web["labels"].(map[string]any)
	if labels["fibe.gg/subdomain"] != "demo-app" {
		t.Fatalf("dotted label path should update whole node, got %#v", labels)
	}

	if strings.Contains(rendered, "variables:") {
		t.Fatalf("compiled compose should not retain x-fibe.gg.variables:\n%s", rendered)
	}
}

func TestApplyLaunchOverridesFailsWhenRandomGenerationFails(t *testing.T) {
	randomBytes := func([]byte) (int, error) {
		return 0, errors.New("entropy unavailable")
	}

	_, err := ApplyOverrides(`x-fibe.gg:
  variables:
    SECRET:
      name: Secret
      random: true
      path: services.web.environment.SECRET
services:
  web:
    image: nginx:alpine
    environment:
      SECRET: placeholder
`, nil, nil, nil, OverrideOptions{RandomBytes: randomBytes})
	if err == nil || !strings.Contains(err.Error(), "random value generation failed") {
		t.Fatalf("expected random generation error, got %v", err)
	}
}

func TestApplyLaunchOverridesFailsWhenRandomGenerationShortReads(t *testing.T) {
	randomBytes := func(buf []byte) (int, error) {
		buf[0] = 1
		return 1, nil
	}

	_, err := ApplyOverrides(`x-fibe.gg:
  variables:
    SECRET:
      name: Secret
      random: true
services:
  web:
    image: nginx:alpine
    environment:
      SECRET: $$random__SECRET
`, nil, nil, nil, OverrideOptions{RandomBytes: randomBytes})
	if err == nil || !strings.Contains(err.Error(), "random value generation failed") || !strings.Contains(err.Error(), "returned 1 bytes") {
		t.Fatalf("expected short random read error, got %v", err)
	}
}

func TestApplyLaunchOverridesCompilesRandomInlineReferences(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  variables:
    TOKEN:
      name: Token
      random: true
services:
  web:
    image: nginx:alpine
    environment:
      TOKEN_REFERENCE: $$random__TOKEN
`, nil, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	if strings.Contains(rendered, "$$random__TOKEN") {
		t.Fatalf("$$random__ reference should be substituted:\n%s", rendered)
	}
	if !regexp.MustCompile(`TOKEN_REFERENCE: ['"]?[a-f0-9]{32}['"]?`).MatchString(rendered) {
		t.Fatalf("$$random__ reference should render a 32-char hex value:\n%s", rendered)
	}
}

func TestApplyLaunchOverridesUsesFibePlaceholderForMissingInlineVariables(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  variables:
    OPTIONAL:
      name: Optional value
services:
  web:
    image: nginx:alpine
    environment:
      OPTIONAL: $$var__OPTIONAL
`, nil, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v\n%s", err, rendered)
	}
	env := doc["services"].(map[string]any)["web"].(map[string]any)["environment"].(map[string]any)
	if env["OPTIONAL"] != "placeholder" {
		t.Fatalf("optional blank inline variable should match FibeCore placeholder fallback, got %#v\n%s", env["OPTIONAL"], rendered)
	}
	if strings.Contains(rendered, "$$var__OPTIONAL") {
		t.Fatalf("rendered compose should not leak unresolved token:\n%s", rendered)
	}
}

func TestApplyLaunchOverridesTreatsBlankTemplateInputsAsMissing(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  variables:
    APP_NAME:
      name: App name
      default: fallback
      validation: "/^[a-z]+$/"
      path: services.web.environment.APP_NAME
    TOKEN:
      name: Token
      default: ""
      random: true
      path: services.web.environment.TOKEN
    SPACED:
      name: Spaced value
      path: services.web.environment.SPACED
    SIGNED:
      name: Signed value
      default: "-1"
      path: services.web.environment.SIGNED
services:
  web:
    image: nginx:alpine
    environment: {}
`, map[string]string{"APP_NAME": "", "SPACED": " true "}, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v\n%s", err, rendered)
	}
	env := doc["services"].(map[string]any)["web"].(map[string]any)["environment"].(map[string]any)
	if env["APP_NAME"] != "fallback" {
		t.Fatalf("blank provided value should fall back to default, got %#v", env["APP_NAME"])
	}
	if token, ok := env["TOKEN"].(string); !ok || len(token) != 32 {
		t.Fatalf("blank default should fall through to random, got %#v", env["TOKEN"])
	}
	if env["SPACED"] != " true " {
		t.Fatalf("non-empty whitespace-containing values should not be trimmed, got %#v", env["SPACED"])
	}
	if env["SIGNED"] != "-1" {
		t.Fatalf("signed digit strings should stay strings, got %#v", env["SIGNED"])
	}
}

func TestApplyLaunchOverridesRejectsBlankRequiredTemplateInput(t *testing.T) {
	_, err := ApplyOverrides(`x-fibe.gg:
  variables:
    APP_NAME:
      name: App name
      required: true
      validation: "/^[a-z]+$/"
      path: services.web.environment.APP_NAME
services:
  web:
    image: nginx:alpine
    environment: {}
`, map[string]string{"APP_NAME": ""}, nil, nil, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), "Variable \"APP_NAME\" is required") {
		t.Fatalf("expected blank required value to fail as missing, got %v", err)
	}
}

func TestApplyLaunchOverridesRejectsUnusedTemplateVariables(t *testing.T) {
	_, err := ApplyOverrides(`x-fibe.gg:
  variables:
    UNUSED:
      name: Unused
      default: value
services:
  web:
    image: nginx:alpine
`, nil, nil, nil, OverrideOptions{})
	if err == nil {
		t.Fatalf("expected unused template variable error")
	}
	if !strings.Contains(err.Error(), "unused template variables: UNUSED") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyLaunchOverridesRejectsUndeclaredInlineVariablesWithoutDefinitions(t *testing.T) {
	assertApplyLaunchOverridesError(t, `services:
  web:
    image: nginx:$$var__TAG
`, nil, "undeclared template variables: TAG")
}

func TestApplyLaunchOverridesRejectsProvidedVariablesWithoutDefinitions(t *testing.T) {
	assertApplyLaunchOverridesError(t, `services:
  web:
    image: nginx:alpine
`, map[string]string{"TAG": "stable"}, `Variable "TAG" is not declared`)
}

func TestApplyLaunchOverridesRejectsWhitespaceTemplateVariableNames(t *testing.T) {
	_, err := ApplyOverrides(`x-fibe.gg:
  variables:
    " TAG ":
      name: Tag
      default: stable
services:
  web:
    image: nginx:$$var__TAG
`, nil, nil, nil, OverrideOptions{})
	if err == nil {
		t.Fatalf("expected whitespace template variable name to fail")
	}
	for _, want := range []string{`Variable " TAG " has invalid name`, "undeclared template variables: TAG"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in error %q", want, err.Error())
		}
	}
}

func TestApplyLaunchOverridesPreservesHostnameWithoutTemplateCompilation(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  metadata:
    description: Demo
services:
  web:
    image: nginx:alpine
    hostname: custom-host
`, nil, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	if !strings.Contains(rendered, "hostname: custom-host") {
		t.Fatalf("non-template compose should preserve hostname:\n%s", rendered)
	}
}

func TestApplyLaunchOverridesCompilesRootDomainToken(t *testing.T) {
	rendered, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
    environment:
      PUBLIC_URL: https://app.$$root_domain
    labels:
      fibe.gg/subdomain: app-$$root_domain
`, nil, nil, nil, OverrideOptions{RootDomain: "example.test"})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	if !strings.Contains(rendered, "PUBLIC_URL: https://app.example.test") {
		t.Fatalf("expected root domain in environment:\n%s", rendered)
	}
	if !strings.Contains(rendered, "fibe.gg/subdomain: app-example.test") {
		t.Fatalf("expected root domain in labels:\n%s", rendered)
	}
}

func TestApplyLaunchOverridesRejectsUnknownServiceInputs(t *testing.T) {
	_, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
`, nil, map[string]string{"missing": "missing-demo"}, nil, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), "service_subdomains.missing") {
		t.Fatalf("expected unknown service_subdomains error, got %v", err)
	}

	_, err = ApplyOverrides(`services:
  web:
    image: nginx:alpine
`, nil, nil, map[string]any{"missing": map[string]any{"subdomain": "missing-demo"}}, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), "services.missing") {
		t.Fatalf("expected unknown launch service override error, got %v", err)
	}

	_, err = ApplyOverrides(`volumes:
  data: {}
`, nil, map[string]string{"missing-service": "missing-demo"}, nil, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), "service overrides require compose services") {
		t.Fatalf("expected missing services-map override error, got %v", err)
	}

	_, err = ApplyOverrides(`services:
  web:
    image: nginx:alpine
`, nil, nil, map[string]any{"web": "not-an-object"}, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), "malformed override objects") {
		t.Fatalf("expected malformed launch service override error, got %v", err)
	}
}

func TestApplyLaunchOverridesRejectsMalformedServiceDefinitions(t *testing.T) {
	_, err := ApplyOverrides(`services:
  web: null
`, nil, nil, nil, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), `compose service "web" must be a mapping`) {
		t.Fatalf("expected malformed service definition error, got %v", err)
	}
}

func TestApplyLaunchOverridesRejectsBlankServiceSubdomains(t *testing.T) {
	for _, tt := range []struct {
		name       string
		subdomains map[string]string
		want       string
	}{
		{
			name:       "blank service",
			subdomains: map[string]string{" ": "demo"},
			want:       "service_subdomains.<blank>",
		},
		{
			name:       "blank subdomain",
			subdomains: map[string]string{"web": " "},
			want:       "service_subdomains.web",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
`, nil, tt.subdomains, nil, OverrideOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected blank service_subdomains error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestApplyLaunchOverridesRejectsSubdomainPathThroughScalar(t *testing.T) {
	_, err := ApplyOverrides(`services:
  alpha:
    image: nginx:alpine
    labels: not-a-map
`, nil, map[string]string{"alpha": "demo"}, nil, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), "service_subdomains.alpha could not be written") {
		t.Fatalf("expected malformed service_subdomains error, got %v", err)
	}
}

func TestApplyLaunchOverridesAppliesServiceSubdomainToListLabels(t *testing.T) {
	rendered, err := ApplyOverrides(`services:
  alpha:
    image: nginx:alpine
    labels:
      - fibe.gg/port=3000
      - com.example.role=edge
`, nil, map[string]string{"alpha": "alpha-demo"}, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	for _, want := range []string{
		"fibe.gg/port: \"3000\"",
		"fibe.gg/subdomain: alpha-demo",
		"com.example.role: edge",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered compose:\n%s", want, rendered)
		}
	}
}

func TestApplyLaunchOverridesRejectsInvalidTemplateVariables(t *testing.T) {
	_, err := ApplyOverrides(`x-fibe.gg:
  variables:
    NAME:
      name: Name
      required: true
      validation: "^[a-z]+$"
services:
  web:
    image: ghcr.io/acme/app:$$var__MISSING
`, nil, nil, nil, OverrideOptions{})
	if err == nil {
		t.Fatalf("expected template validation error")
	}
	text := err.Error()
	for _, want := range []string{"Variable \"NAME\" is required", "validation must be wrapped", "undeclared template variables", "unused template variables"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in error %q", want, text)
		}
	}
}

func TestApplyLaunchOverridesRejectsInvalidTemplateVariableFieldTypes(t *testing.T) {
	cases := []struct {
		name       string
		definition string
		want       string
	}{
		{
			name: "name number",
			definition: `name: 123
path: services.web.environment.VALUE`,
			want: `Variable "VALUE" name must be a string`,
		},
		{
			name: "required string",
			definition: `name: Value
required: "true"
path: services.web.environment.VALUE`,
			want: `Variable "VALUE" required must be a boolean`,
		},
		{
			name: "random string",
			definition: `name: Value
random: "true"
path: services.web.environment.VALUE`,
			want: `Variable "VALUE" random must be a boolean`,
		},
		{
			name: "default list",
			definition: `name: Value
default: [one]
path: services.web.environment.VALUE`,
			want: `Variable "VALUE" default must be a string, number, boolean, or null`,
		},
		{
			name: "validation boolean",
			definition: `name: Value
validation: true
path: services.web.environment.VALUE`,
			want: `Variable "VALUE" validation must be a string`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ApplyOverrides(`x-fibe.gg:
  variables:
    VALUE:
      `+strings.ReplaceAll(tc.definition, "\n", "\n      ")+`
services:
  web:
    image: nginx:alpine
    environment: {}
`, nil, nil, nil, OverrideOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestApplyLaunchOverridesRejectsMalformedTemplateVariablesBlock(t *testing.T) {
	for _, tt := range []struct {
		name      string
		variables string
	}{
		{name: "scalar", variables: "not-an-object"},
		{name: "list", variables: "[]"},
		{name: "null", variables: "null"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyOverrides(`x-fibe.gg:
  variables: `+tt.variables+`
services:
  web:
    image: nginx:alpine
`, nil, nil, nil, OverrideOptions{})
			if err == nil || !strings.Contains(err.Error(), "x-fibe.gg.variables must be an object") {
				t.Fatalf("expected malformed variables block error, got %v", err)
			}
		})
	}
}

func TestApplyLaunchOverridesSupportsRubyStyleLookaheadValidation(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  variables:
    PASSWORD:
      name: Password
      validation: "/^(?=.*\\d).{10,}$/"
      path: services.web.environment.PASSWORD
    PORT:
      name: Port
      validation: "/^(?!21$|22$)\\d{1,5}$/"
      path: services.web.environment.PORT
services:
  web:
    image: nginx:alpine
    environment: {}
`, map[string]string{"PASSWORD": "abcdefghi1", "PORT": "8081"}, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("ruby-style lookahead validation should pass: %v", err)
	}
	if !strings.Contains(rendered, "PASSWORD: abcdefghi1") || !strings.Contains(rendered, "PORT: 8081") {
		t.Fatalf("expected validated values in rendered compose:\n%s", rendered)
	}

	_, err = ApplyOverrides(`x-fibe.gg:
  variables:
    PASSWORD:
      name: Password
      validation: "/^(?=.*\\d).{10,}$/"
      path: services.web.environment.PASSWORD
services:
  web:
    image: nginx:alpine
    environment: {}
`, map[string]string{"PASSWORD": "abcdefghij"}, nil, nil, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), `variable "PASSWORD" fails validation pattern`) {
		t.Fatalf("expected failed lookahead validation, got %v", err)
	}

	_, err = ApplyOverrides(`x-fibe.gg:
  variables:
    PORT:
      name: Port
      validation: "/^(?!21$|22$)\\d{1,5}$/"
      path: services.web.environment.PORT
services:
  web:
    image: nginx:alpine
    environment: {}
`, map[string]string{"PORT": "22"}, nil, nil, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), `variable "PORT" fails validation pattern`) {
		t.Fatalf("expected failed negative-lookahead validation, got %v", err)
	}
}

func TestApplyLaunchOverridesRejectsInvalidTemplateValidationRegex(t *testing.T) {
	assertApplyLaunchOverridesError(t, `x-fibe.gg:
  variables:
    VALUE:
      name: Value
      validation: "/[[/"
      path: services.web.environment.VALUE
services:
  web:
    image: nginx:alpine
    environment: {}
`, map[string]string{"VALUE": "anything"}, `variable "VALUE" validation regex is invalid`)
}

func TestApplyLaunchOverridesRejectsRootDomainWithoutMarquee(t *testing.T) {
	_, err := ApplyOverrides(`services:
  web:
    image: nginx:alpine
    environment:
      PUBLIC_URL: https://app.$$root_domain
`, nil, nil, nil, OverrideOptions{})
	if err == nil {
		t.Fatalf("expected root domain error")
	}
	if !strings.Contains(err.Error(), "$$root_domain requires a selected Marquee") {
		t.Fatalf("expected root domain error, got %q", err.Error())
	}
}

func TestApplyLaunchOverridesSupportsScalarPaths(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  variables:
    APP_NAME:
      name: App name
      default: demo
      paths: services.web.environment.APP_NAME
services:
  web:
    image: nginx
    environment: {}
`, nil, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	doc := composetest.RenderedService(t, rendered, "web")
	env := doc["environment"].(map[string]any)
	if env["APP_NAME"] != "demo" {
		t.Fatalf("scalar paths should write environment value, got %#v", env)
	}
}

func TestApplyLaunchOverridesRejectsInvalidTemplatePathShapes(t *testing.T) {
	cases := map[string]struct {
		compose string
		want    string
	}{
		"path object": {
			compose: `x-fibe.gg:
  variables:
    APP_NAME:
      name: App name
      default: demo
      path:
        services.web.environment.APP_NAME: ignored
services:
  web:
    image: nginx
    environment: {}
`,
			want: `Variable "APP_NAME" path must be a string`,
		},
		"paths object item": {
			compose: `x-fibe.gg:
  variables:
    APP_NAME:
      name: App name
      default: demo
      paths:
        - services.web.environment.APP_NAME
        - {services.web.environment.BAD: ignored}
services:
  web:
    image: nginx
    environment: {}
`,
			want: `Variable "APP_NAME" paths[1] must be a string`,
		},
		"blank path": {
			compose: `x-fibe.gg:
  variables:
    APP_NAME:
      name: App name
      default: demo
      path: ""
services:
  web:
    image: nginx
    environment: {}
`,
			want: `Variable "APP_NAME" path must be a non-empty template path`,
		},
		"invalid path syntax": {
			compose: `x-fibe.gg:
  variables:
    APP_NAME:
      name: App name
      default: demo
      path: "services.web.environment.APP NAME"
services:
  web:
    image: nginx
    environment: {}
`,
			want: `Variable "APP_NAME" path must match Fibe template path syntax`,
		},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			_, err := ApplyOverrides(tc.compose, nil, nil, nil, OverrideOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestApplyLaunchOverridesSupportsTemplatePathArrayIndexes(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  variables:
    FIRST_PORT:
      name: First port
      default: "8080:80"
      path: services.web.ports.0
    SECOND_PORT:
      name: Second port
      default: "3000:3000"
      path: services.web.ports.[1]
services:
  web:
    image: nginx
    ports:
      - "80:80"
`, nil, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	doc := composetest.RenderedService(t, rendered, "web")
	ports := doc["ports"].([]any)
	if len(ports) != 2 || ports[0] != "8080:80" || ports[1] != "3000:3000" {
		t.Fatalf("template paths should write array indexes, got %#v", ports)
	}
}

func TestApplyLaunchOverridesRejectsMissingServicePathRoots(t *testing.T) {
	assertApplyLaunchOverridesError(t, `x-fibe.gg:
  variables:
    APP_NAME:
      name: App name
      default: demo
      path: services.api.environment.APP_NAME
services:
  web:
    image: nginx
`, nil, "APP_NAME:api")
}

func TestApplyLaunchOverridesRejectsTemplatePathThroughScalar(t *testing.T) {
	assertApplyLaunchOverridesError(t, `x-fibe.gg:
  variables:
    IMAGE_DETAIL:
      name: Image detail
      default: latest
      path: services.web.image.nested.value
services:
  web:
    image: nginx
`, nil, `Variable "IMAGE_DETAIL" path "services.web.image.nested.value" could not be written`)
}

func TestApplyLaunchOverridesStripsTemplateHostnames(t *testing.T) {
	rendered, err := ApplyOverrides(`x-fibe.gg:
  variables: {}
services:
  web:
    image: nginx
    hostname: custom-host
`, nil, nil, nil, OverrideOptions{})
	if err != nil {
		t.Fatalf("apply launch overrides: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v\n%s", err, rendered)
	}
	web := doc["services"].(map[string]any)["web"].(map[string]any)
	if _, ok := web["hostname"]; ok {
		t.Fatalf("template compiler should strip hostname, got %#v", web)
	}
}

func assertApplyLaunchOverridesError(t *testing.T, compose string, variables map[string]string, want string) {
	t.Helper()
	_, err := ApplyOverrides(compose, variables, nil, nil, OverrideOptions{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}

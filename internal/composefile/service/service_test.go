package service

import (
	"errors"
	"testing"
)

func TestAppendMiddlewareIgnoresMalformedExistingValue(t *testing.T) {
	if got := appendMiddleware(123, "pg-one-web-redirect"); got != "pg-one-web-redirect" {
		t.Fatalf("appendMiddleware(123) = %q; want generated middleware only", got)
	}
}

func TestOverrideScalarReadersRejectUnsupportedShapes(t *testing.T) {
	values := map[string]any{
		"text":   " value ",
		"number": 80,
		"list":   []any{"nginx"},
		"object": map[string]any{"image": "nginx"},
		"bool":   true,
	}
	if got := overrideString(values, "text"); got != "value" {
		t.Fatalf("overrideString text = %q; want trimmed string", got)
	}
	if got := overridePortString(values, "number"); got != "80" {
		t.Fatalf("overridePortString number = %q; want numeric port text", got)
	}
	for _, key := range []string{"number", "list", "object", "bool"} {
		if got := overrideString(values, key); got != "" {
			t.Fatalf("overrideString(%s) = %q; want empty unsupported shape", key, got)
		}
	}
	for _, key := range []string{"text", "list", "object", "bool"} {
		if got := overridePortString(values, key); got != "" {
			t.Fatalf("overridePortString(%s) = %q; want empty unsupported shape", key, got)
		}
	}
}

func TestApplyServiceSubdomainsErrorsKeepTextAndSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rendered  map[string]any
		overrides map[string]string
		want      string
		target    error
	}{
		{
			name:      "missing services",
			rendered:  map[string]any{},
			overrides: map[string]string{"alpha": "api-demo"},
			want:      "path was not found or is not traversable",
			target:    errSubdomainPathNotTraversable,
		},
		{
			name:      "blank value",
			rendered:  map[string]any{"services": map[string]any{"alpha": map[string]any{"image": "nginx"}}},
			overrides: map[string]string{"alpha": ""},
			want:      "service_subdomains.alpha could not be written: value must not be blank",
			target:    errBlankSubdomainValue,
		},
		{
			name:      "missing service",
			rendered:  map[string]any{"services": map[string]any{}},
			overrides: map[string]string{"alpha": "api-demo"},
			want:      "service_subdomains.alpha could not be written: path was not found or is not traversable",
			target:    errSubdomainPathNotTraversable,
		},
		{
			name:      "malformed labels",
			rendered:  map[string]any{"services": map[string]any{"alpha": map[string]any{"labels": 123}}},
			overrides: map[string]string{"alpha": "api-demo"},
			want:      "service_subdomains.alpha could not be written: path was not found or is not traversable",
			target:    errSubdomainPathNotTraversable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ApplyServiceSubdomains(tt.rendered, tt.overrides)
			if err == nil {
				t.Fatal("ApplyServiceSubdomains returned nil error")
			}
			if err.Error() != tt.want {
				t.Fatalf("error text = %q; want %q", err.Error(), tt.want)
			}
			if !errors.Is(err, tt.target) {
				t.Fatalf("errors.Is(%v, %v) = false", err, tt.target)
			}
		})
	}
}

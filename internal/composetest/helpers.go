package composetest

import "gopkg.in/yaml.v3"

// TB is the subset of testing.TB used by Compose test helpers.
type TB interface {
	// Helper marks the calling function as a test helper.
	Helper()
	// Fatalf fails the test immediately.
	Fatalf(format string, args ...any)
}

// RenderedService parses rendered Compose YAML and returns one service map.
func RenderedService(t TB, rendered string, serviceName string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("parse rendered compose: %v\n%s", err, rendered)
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		t.Fatalf("rendered compose has no services map:\n%s", rendered)
	}
	service, ok := services[serviceName].(map[string]any)
	if !ok {
		t.Fatalf("rendered compose has no service %q:\n%s", serviceName, rendered)
	}
	return service
}

// AssertRenderedCommand verifies one service command is an exact exec-form array.
func AssertRenderedCommand(t TB, rendered string, serviceName string, want []string) {
	t.Helper()
	command := RenderedService(t, rendered, serviceName)["command"]
	values, ok := command.([]any)
	if !ok {
		t.Fatalf("expected %s command as exec-form array, got %#v\n%s", serviceName, command, rendered)
	}
	if len(values) != len(want) {
		t.Fatalf("unexpected command length: got %#v want %#v", values, want)
	}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("unexpected command at %d: got %#v want %#v", i, values[i], want[i])
		}
	}
}

// RenderedVolumeMapAtTarget returns the long-form volume at a target path.
func RenderedVolumeMapAtTarget(t TB, service map[string]any, target string) map[string]any {
	t.Helper()
	volumes, ok := service["volumes"].([]any)
	if !ok {
		t.Fatalf("service has no volume list: %#v", service)
	}
	for _, item := range volumes {
		values, ok := item.(map[string]any)
		if ok && values["target"] == target {
			return values
		}
	}
	t.Fatalf("service has no volume target %q: %#v", target, service["volumes"])
	return nil
}

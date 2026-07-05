package template

import "testing"

func TestSplitTemplatePathPreservesWhitespaceInsideSegments(t *testing.T) {
	parts := splitTemplatePath(`services.web.environment.APP\. NAME`)
	want := []string{"services", "web", "environment", "APP. NAME"}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("parts[%d] = %#v, want %#v (all parts %#v)", i, parts[i], want[i], parts)
		}
	}

	parts = splitTemplatePath(`services.web.ports.[-1]`)
	if got := parts[len(parts)-1]; got != "[-1]" {
		t.Fatalf("signed bracket index should stay a key segment, got %#v in %#v", got, parts)
	}
}

package optfibe

import "testing"

func TestSourceCheckoutPathBuildsStableRepositoryBranchPath(t *testing.T) {
	got := SourceCheckoutPath("demo--1", "https://github.com/acme/my-api.git", "feature/new ui")
	want := "/opt/fibe/playgrounds/demo--1/props/acme-my-api/feature-new-ui"
	if got != want {
		t.Fatalf("SourceCheckoutPath() = %q, want %q", got, want)
	}
}

func TestSourceCheckoutPathDefaultsBranch(t *testing.T) {
	got := SourceCheckoutPath("demo--1", "git@github.com:acme/private.git", "")
	want := "/opt/fibe/playgrounds/demo--1/props/acme-private/main"
	if got != want {
		t.Fatalf("SourceCheckoutPath() = %q, want %q", got, want)
	}
}

func TestSafePathComponentHardensTraversalComponents(t *testing.T) {
	cases := map[string]string{
		"feature/new ui": "feature-new-ui",
		"../secrets":     "secrets",
		"..":             "source",
		"API@Feature":    "api-feature",
	}
	for raw, want := range cases {
		if got := safePathComponent(raw); got != want {
			t.Fatalf("safePathComponent(%q) = %q, want %q", raw, got, want)
		}
	}
}

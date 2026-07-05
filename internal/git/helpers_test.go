package git

import "testing"

func TestSameRepositoryURLPreservesGenericPathCase(t *testing.T) {
	if SameRepositoryURL("https://example.com/acme/api.git", "https://example.com/acme/api/") != true {
		t.Fatalf("expected generic repository spelling variants to match")
	}
	if SameRepositoryURL("HTTPS://EXAMPLE.COM/acme/api.git", "https://example.com/acme/api") != true {
		t.Fatalf("expected generic repository scheme and host case to normalize")
	}
	if SameRepositoryURL("https://example.com/Acme/api.git", "https://example.com/acme/api.git") {
		t.Fatalf("generic repository path case must remain significant")
	}
	if SameRepositoryURL("git@example.com:Acme/api.git", "git@example.com:acme/api.git") {
		t.Fatalf("generic scp-like repository path case must remain significant")
	}
	if !SameRepositoryURL("git@EXAMPLE.COM:acme/api.git", "git@example.com:acme/api") {
		t.Fatalf("expected generic scp-like repository host case to normalize")
	}
	if SameRepositoryURL("Git@example.com:acme/api.git", "git@example.com:acme/api.git") {
		t.Fatalf("generic scp-like repository user case must remain significant")
	}
}

func TestSameRepositoryURLKeepsGitHubCaseInsensitive(t *testing.T) {
	if !SameRepositoryURL("https://github.com/Acme/API.git", "git@github.com:acme/api.git") {
		t.Fatalf("expected GitHub repository identity to stay case-insensitive")
	}
}

func TestRepositoryFullNameRejectsUnsupportedSchemes(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/acme/api.git",
		"ssh://git@github.com/acme/api.git",
		"git@github.com:acme/api.git",
		"acme/api",
	} {
		if _, ok := RepositoryFullName(raw); !ok {
			t.Fatalf("expected GitHub repository identity for %q", raw)
		}
	}
	for _, raw := range []string{
		"http://github.com/acme/api.git",
		"git://github.com/acme/api.git",
		"ftp://github.com/acme/api.git",
		"git+ssh://github.com/acme/api.git",
		"https://github.com/acme/api.git?tab=readme",
		"https://github.com/acme/api.git#readme",
		"ssh://git@github.com/acme/api.git?depth=1",
		"https://github.com/../api.git",
		"https://github.com/acme/..",
		"https://github.com/acme/%2e%2e",
		"../api",
		"acme/..",
	} {
		if fullName, ok := RepositoryFullName(raw); ok {
			t.Fatalf("expected unsupported GitHub repository identity %q to be rejected, got %q", raw, fullName)
		}
	}
}

func TestCloneableRepositoryURLRejectsGitHubShorthand(t *testing.T) {
	for _, raw := range []string{
		"http://git.example.test/acme/api.git",
		"https://github.com/acme/api.git",
		"ssh://git.example.test/acme/api.git",
		"ssh://git@github.com/acme/api.git",
		"git://git.example.test/acme/api.git",
		"git@github.com:acme/api.git",
		"git@example.com:Acme/API.git",
	} {
		if !CloneableRepositoryURL(raw) {
			t.Fatalf("expected cloneable repository target %q", raw)
		}
	}
	for _, raw := range []string{
		"",
		"acme/api",
		"acme/api@main",
		"github.com/acme/api",
		"http://github.com/acme/api.git",
		"git://github.com/acme/api.git",
		"https://github.com/acme/api/extra",
		"git@github.com:acme/api/extra.git",
		"file:///tmp/acme/api.git",
		"ftp://git.example.test/acme/api.git",
		"foo://git.example.test/acme/api.git",
		"git+ssh://git.example.test/acme/api.git",
		"http://git.example.test",
		"https://git.example.test/",
		"ssh://git.example.test",
		"git://git.example.test",
		"git@example.com:/",
		"git@example.com:////",
		"https://github.com/acme/api.git?tab=readme",
		"https://github.com/acme/api.git#readme",
		"ssh://git@github.com/acme/api.git?depth=1",
		"https://git.example.test/acme/api.git?tab=readme",
		"ssh://git.example.test/acme/api.git#readme",
		"git@example.com:acme/api.git?depth=1",
		"git@example.com:acme/api.git#readme",
		"https://git.example.test/../api.git",
		"https://git.example.test/acme/../api.git",
		"https://git.example.test/acme/%2e%2e/api.git",
		"https://git.example.test/acme/./api.git",
		"git@example.com:../api.git",
		"git@example.com:acme/../api.git",
		"git@example.com:acme/./api.git",
		"https://github.com/acme/api with space.git",
		"git@example.com:acme/api with space.git",
	} {
		if CloneableRepositoryURL(raw) {
			t.Fatalf("expected non-cloneable repository target %q", raw)
		}
	}
}

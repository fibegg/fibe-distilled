package git

import "testing"

func TestRepositoryCredentialsRedaction(t *testing.T) {
	raw := "https://user:secret@example.com/acme/api.git"
	if !RepositoryURLHasCredentials(raw) {
		t.Fatalf("expected credential-bearing repository URL to be detected")
	}
	if got := RedactRepositoryCredentials(raw); got != "https://***@example.com/acme/api.git" {
		t.Fatalf("unexpected redacted URL %q", got)
	}
}

func TestRepositoryCredentialsIgnoresSSHUser(t *testing.T) {
	raw := "ssh://git@example.com/acme/api.git"
	if RepositoryURLHasCredentials(raw) {
		t.Fatalf("ssh user should not be treated as embedded credentials")
	}
	if got := RedactRepositoryCredentials(raw); got != raw {
		t.Fatalf("unexpected SSH URL rewrite %q", got)
	}
}

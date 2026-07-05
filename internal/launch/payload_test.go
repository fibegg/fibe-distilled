package launch

import "testing"

func TestNameFromRepoNormalizesGitSuffixBeforeDefaulting(t *testing.T) {
	t.Parallel()

	if got := nameFromRepo(""); got != "" {
		t.Fatalf("empty repository default name = %q, want blank", got)
	}

	for _, repo := range []string{
		"https://git.example.test/acme/demo.git/",
		"git@git.example.test:acme/demo.git/",
		"acme/demo.git/",
	} {
		t.Run(repo, func(t *testing.T) {
			t.Parallel()
			if got := nameFromRepo(repo); got != "demo" {
				t.Fatalf("nameFromRepo(%q) = %q, want demo", repo, got)
			}
		})
	}
}

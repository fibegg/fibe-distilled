package launch

import (
	"strings"
	"testing"
)

func TestGitHubRepositoryURLsUsesCanonicalGitHubRepositoryIdentity(t *testing.T) {
	got := GitHubRepositoryURLs(`services:
  https:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/api.git
  ssh_url:
    image: node:22
    labels:
      fibe.gg/repo_url: ssh://git@github.com/acme/ssh-url.git
  ssh:
    image: node:22
    labels:
      fibe.gg/repo_url: git@github.com:acme/worker.git
  shorthand:
    image: node:22
    labels:
      fibe.gg/repo_url: acme/shorthand
  http_github:
    image: node:22
    labels:
      fibe.gg/repo_url: http://github.com/acme/insecure.git
  git_scheme_github:
    image: node:22
    labels:
      fibe.gg/repo_url: git://github.com/acme/read.git
  malformed_github:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com/acme/api/extra.git
  impostor:
    image: node:22
    labels:
      fibe.gg/repo_url: https://notgithub.com/acme/api.git
  suffix:
    image: node:22
    labels:
      fibe.gg/repo_url: https://github.com.evil/acme/api.git
`)
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"https://github.com/acme/api.git",
		"ssh://git@github.com/acme/ssh-url.git",
		"git@github.com:acme/worker.git",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in GitHub repos %#v", want, got)
		}
	}
	for _, unwanted := range []string{"notgithub.com", "github.com.evil", "acme/shorthand", "http://github.com", "git://github.com", "api/extra"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("unexpected impostor host %q in GitHub repos %#v", unwanted, got)
		}
	}
}

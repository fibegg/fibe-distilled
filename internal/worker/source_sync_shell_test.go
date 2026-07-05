package worker

import (
	"net/url"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/git"
	"github.com/fibegg/fibe-distilled/internal/runtime"
)

const sourceSyncScript = `git_auth() {
  if [ -n "$auth_header" ]; then
    git -c "http.extraHeader=$auth_header" "$@"
  else
    git "$@"
  fi
}
mkdir -p "$parent"
if [ -d "$target/.git" ]; then
  dirty="$(git -C "$target" status --porcelain)"
  if [ -n "$dirty" ]; then printf '%s\n' "fibe_distilled_source_sync_category=dirty_work" "fibe_distilled_source_sync_dirty_entries=$(printf '%s\n' "$dirty" | wc -l | tr -d ' ')" >&2; exit 20; fi
  git -C "$target" remote set-url origin "$repo"
  git_auth -C "$target" fetch --all --prune
  if ! git -C "$target" rev-parse --verify "origin/$branch" >/dev/null 2>&1; then printf '%s\n' "fibe_distilled_source_sync_category=missing_upstream" >&2; exit 21; fi
  if ! git -C "$target" checkout "$branch"; then git -C "$target" checkout -b "$branch" "origin/$branch" || { printf '%s\n' "fibe_distilled_source_sync_category=checkout_failed" >&2; exit 22; }; fi
  counts="$(git -C "$target" rev-list --left-right --count HEAD..."origin/$branch" 2>/dev/null)" || { printf '%s\n' "fibe_distilled_source_sync_category=history_unverifiable" >&2; exit 25; }
  set -- $counts
  if [ "$#" -ne 2 ]; then printf '%s\n' "fibe_distilled_source_sync_category=history_unverifiable" >&2; exit 25; fi
  ahead="$1"; behind="$2"
  case "$ahead" in ''|*[!0-9]*) printf '%s\n' "fibe_distilled_source_sync_category=history_unverifiable" >&2; exit 25;; esac
  case "$behind" in ''|*[!0-9]*) printf '%s\n' "fibe_distilled_source_sync_category=history_unverifiable" >&2; exit 25;; esac
  ahead_nonzero=0; behind_nonzero=0
  case "$ahead" in *[1-9]*) ahead_nonzero=1;; esac
  case "$behind" in *[1-9]*) behind_nonzero=1;; esac
  if [ "$ahead_nonzero" = 1 ] && [ "$behind_nonzero" = 1 ]; then printf '%s\n' "fibe_distilled_source_sync_category=diverged" "fibe_distilled_source_sync_ahead=$ahead" "fibe_distilled_source_sync_behind=$behind" >&2; exit 23; fi
  if [ "$ahead_nonzero" = 1 ]; then printf '%s\n' "fibe_distilled_source_sync_category=ahead" "fibe_distilled_source_sync_ahead=$ahead" >&2; exit 24; fi
  git_auth -C "$target" pull --ff-only
else
  git_auth clone --branch "$branch" "$repo" "$target"
  git -C "$target" remote set-url origin "$repo"
fi`

func sourceSyncCommand(target runtime.RemoteCheckoutPath, repoURL string, authHeader string, branch string) string {
	repoURL = sourceSyncRemoteURL(repoURL)
	assignments := strings.Join([]string{
		"GIT_TERMINAL_PROMPT=0",
		"parent=" + runtime.ShellQuote(target.Parent()),
		"target=" + runtime.ShellQuote(target.String()),
		"repo=" + runtime.ShellQuote(repoURL),
		"auth_header=" + runtime.ShellQuote(authHeader),
		"branch=" + runtime.ShellQuote(branch),
	}, " ")
	return assignments + " sh -eu <<'FIBE_DISTILLED_SOURCE_SYNC'\n" + sourceSyncScript + "\nFIBE_DISTILLED_SOURCE_SYNC"
}

func sourceSyncRemoteURL(raw string) string {
	if !git.RepositoryURLHasCredentials(raw) {
		return raw
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

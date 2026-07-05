package worker

import (
	"context"
	"slices"
	"strings"
	"sync"

	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/git"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
	"github.com/fibegg/fibe-distilled/internal/runtime"
)

// syncSources clones or updates all repository-backed services for a Playspec.
func (w Worker) syncSources(ctx context.Context, marquee domain.Marquee, project string, ps domain.Playspec) error {
	summaries, err := validatedSourceServices(ps.BaseComposeYAML, "source sync")
	if err != nil {
		return err
	}
	plans, err := w.sourceSyncPlans(project, summaries)
	if err != nil {
		return err
	}
	return w.runSourceSyncPlans(ctx, marquee, plans)
}

// sourceSyncPlan contains one remote checkout operation.
type sourceSyncPlan struct {
	project    string
	service    string
	repoURL    string
	branch     string
	targetPath runtime.RemoteCheckoutPath
}

// sourceSyncPlan builds a remote checkout plan for one service summary.
func (w Worker) sourceSyncPlan(project string, summary service.Summary) (sourceSyncPlan, bool, error) {
	if summary.RepoURL == "" {
		return sourceSyncPlan{}, false, nil
	}
	branch := service.SourceBranch(summary)
	targetPath, err := runtime.NewRemoteCheckoutPath(project, optfibe.SourceCheckoutPath(project, summary.RepoURL, branch))
	if err != nil {
		return sourceSyncPlan{}, false, err
	}
	return sourceSyncPlan{
		project:    project,
		service:    summary.Name,
		repoURL:    summary.RepoURL,
		branch:     branch,
		targetPath: targetPath,
	}, true, nil
}

// sourceSyncPlans builds remote checkout plans from service summaries.
func (w Worker) sourceSyncPlans(project string, summaries []service.Summary) ([]sourceSyncPlan, error) {
	var plans []sourceSyncPlan
	for _, summary := range summaries {
		plan, ok, err := w.sourceSyncPlan(project, summary)
		if err != nil {
			return nil, err
		}
		if ok {
			plans = append(plans, plan)
		}
	}
	return plans, nil
}

// runSourceSyncPlans executes remote checkout plans in order.
func (w Worker) runSourceSyncPlans(ctx context.Context, marquee domain.Marquee, plans []sourceSyncPlan) error {
	for _, plan := range plans {
		if err := sourceSyncLocks.withLock(plan.targetPath.String(), func() error {
			return w.runSourceSync(ctx, marquee, plan)
		}); err != nil {
			return err
		}
	}
	return nil
}

// sourceSyncLocks serializes source syncs by remote checkout path.
var sourceSyncLocks keyedSourceSyncLocks

// keyedSourceSyncLocks stores per-checkout mutexes.
type keyedSourceSyncLocks struct {
	mu    sync.Mutex
	locks map[string]*sourceSyncLock
}

// sourceSyncLock tracks waiters for one checkout mutex.
type sourceSyncLock struct {
	mu   sync.Mutex
	refs int
}

// withLock serializes source-sync commands that target the same checkout path.
func (l *keyedSourceSyncLocks) withLock(key string, run func() error) error {
	unlock := l.lock(key)
	defer unlock()
	return run()
}

// lock returns a release function for a keyed source-sync lock.
func (l *keyedSourceSyncLocks) lock(key string) func() {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[string]*sourceSyncLock{}
	}
	entry := l.locks[key]
	if entry == nil {
		entry = &sourceSyncLock{}
		l.locks[key] = entry
	}
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.locks, key)
		}
		l.mu.Unlock()
	}
}

// runSourceSync executes one source sync plan on the Marquee.
func (w Worker) runSourceSync(ctx context.Context, marquee domain.Marquee, plan sourceSyncPlan) error {
	err := w.Runtime.SyncSource(ctx, marquee, runtime.GitSyncRequest{
		Project:     plan.project,
		Service:     plan.service,
		RepoURL:     plan.repoURL,
		Branch:      plan.branch,
		TargetPath:  plan.targetPath,
		GitHubToken: w.DefaultGitHubToken,
	})
	if err != nil {
		return classifySourceSyncError(plan.service, plan.repoURL, plan.branch, plan.targetPath.String(), runtime.CommandResult{Stderr: err.Error()}, err)
	}
	return nil
}

// sourceSyncError preserves structured details for source-sync failures.
type sourceSyncError struct {
	Service       string
	RepositoryURL string
	Branch        string
	Path          string
	Category      string
	Message       string
	Output        string
	Err           error
}

// Error returns the user-facing source-sync failure message.
func (e sourceSyncError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "source sync failed"
}

// Unwrap returns the lower-level source-sync failure.
func (e sourceSyncError) Unwrap() error {
	return e.Err
}

// Details returns structured API details for a source-sync failure.
func (e sourceSyncError) Details() map[string]any {
	out := map[string]any{
		"category":       e.Category,
		"service":        e.Service,
		"repository_url": git.RedactRepositoryCredentials(e.RepositoryURL),
		"branch":         e.Branch,
		"path":           e.Path,
		"message":        e.Message,
	}
	if strings.TrimSpace(e.Output) != "" {
		out["output"] = strings.TrimSpace(e.Output)
	}
	return out
}

// PreservesWork reports failures that should not overwrite local checkout state.
func (e sourceSyncError) PreservesWork() bool {
	switch e.Category {
	case "source_sync_dirty_work", "source_sync_ahead", "source_sync_diverged", "source_sync_missing_upstream", "source_sync_checkout_failed", "source_sync_history_unverifiable":
		return true
	default:
		return false
	}
}

// NextActions returns operator guidance for recoverable source-sync failures.
func (e sourceSyncError) NextActions() []string {
	if actions, ok := sourceSyncNextActions[e.Category]; ok {
		return slices.Clone(actions)
	}
	return slices.Clone(defaultSourceSyncNextActions)
}

// sourceSyncNextActions maps source-sync failure categories to operator guidance.
var sourceSyncNextActions = map[string][]string{
	"source_sync_dirty_work":           {"commit or discard local changes before source sync can update the branch"},
	"source_sync_ahead":                {"push local commits or reset the branch before source sync can pull"},
	"source_sync_diverged":             {"resolve branch divergence manually before source sync can continue"},
	"source_sync_missing_upstream":     {"check that the configured branch still exists on the remote repository"},
	"source_sync_history_unverifiable": {"inspect the source checkout before source sync can prove branch state"},
	"source_sync_git_auth":             {"check GitHub token access for the repository"},
	"source_sync_network":              {"check Marquee network access to the Git remote"},
}

// defaultSourceSyncNextActions is the fallback source-sync operator guidance.
var defaultSourceSyncNextActions = []string{"inspect the source checkout and retry source sync"}

// classifySourceSyncError turns Git failure output into a structured error.
func classifySourceSyncError(service string, repoURL string, branch string, target string, result runtime.CommandResult, err error) error {
	output := redactGitCredentials(strings.TrimSpace(result.Stdout + "\n" + result.Stderr))
	category := sourceSyncCategory(output)
	message := sourceSyncMessage(category)
	if message == "" {
		message = "Source sync failed"
	}
	if strings.TrimSpace(output) != "" {
		message += ": " + firstLine(output)
	}
	return sourceSyncError{
		Service:       service,
		RepositoryURL: repoURL,
		Branch:        branch,
		Path:          target,
		Category:      category,
		Message:       message,
		Output:        output,
		Err:           err,
	}
}

// sourceSyncCategory chooses a stable category for source-sync output.
func sourceSyncCategory(output string) string {
	if category := matchSourceSyncMarker(output); category != "" {
		return "source_sync_" + category
	}
	lower := strings.ToLower(output)
	for _, match := range sourceSyncCategoryMatches {
		if containsAny(lower, match.needles) {
			return match.category
		}
	}
	return "source_sync_failed"
}

// sourceSyncCategoryMatch maps output fragments to a source-sync category.
type sourceSyncCategoryMatch struct {
	category string
	needles  []string
}

// sourceSyncCategoryMatches handles common Git auth, missing repo, and network failures.
var sourceSyncCategoryMatches = []sourceSyncCategoryMatch{
	{category: "source_sync_git_auth", needles: []string{"authentication failed", "could not read username", "terminal prompts disabled", "permission denied"}},
	{category: "source_sync_repository_not_found", needles: []string{"repository not found", "not found"}},
	{category: "source_sync_network", needles: []string{"could not resolve host", "failed to connect", "connection reset", "timed out"}},
}

// matchSourceSyncMarker extracts fibe-distilled markers emitted by the remote script.
func matchSourceSyncMarker(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if category, ok := strings.CutPrefix(line, "fibe_distilled_source_sync_category="); ok {
			return strings.TrimSpace(category)
		}
	}
	return ""
}

// sourceSyncMessage returns a concise user-facing message for a category.
func sourceSyncMessage(category string) string {
	if message := sourceSyncMessages[category]; message != "" {
		return message
	}
	return "Source sync failed"
}

// sourceSyncMessages maps remote sync markers to user-facing messages.
var sourceSyncMessages = map[string]string{
	"source_sync_dirty_work":           "Source sync skipped to preserve dirty local work",
	"source_sync_ahead":                "Source sync skipped because the local branch is ahead of upstream",
	"source_sync_diverged":             "Source sync skipped because the local branch diverged from upstream",
	"source_sync_missing_upstream":     "Source sync failed because the upstream branch is missing",
	"source_sync_checkout_failed":      "Source sync failed because the target branch could not be checked out",
	"source_sync_history_unverifiable": "Source sync skipped because branch history could not be verified",
	"source_sync_git_auth":             "Source sync failed because Git authentication failed",
	"source_sync_repository_not_found": "Source sync failed because the repository was not found",
	"source_sync_network":              "Source sync failed because the Git remote was unreachable",
}

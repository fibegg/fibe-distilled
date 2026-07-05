package worker

import (
	"context"
	"errors"
	"maps"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/buildrecord"
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/git"
	"github.com/fibegg/fibe-distilled/internal/playguard"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

const (
	// webhookBuildReadyReason marks services ready after a webhook-triggered build.
	webhookBuildReadyReason = "webhook_build_ready"
	// webhookBuildFailedReason marks services that failed during a webhook-triggered build.
	webhookBuildFailedReason = "webhook_build_failed"
)

// GitHubPushEvent is the normalized subset of a GitHub push delivery.
type GitHubPushEvent struct {
	// RepositoryFullName is the owner/repository GitHub identity.
	RepositoryFullName string
	// Branch is the pushed branch name.
	Branch string
	// After is the pushed commit SHA after the update.
	After string
}

// GitHubPushResult summarizes webhook-triggered runtime work.
type GitHubPushResult struct {
	// MatchedPlaygrounds counts Playgrounds affected by the push.
	MatchedPlaygrounds int
	// SyncedSources counts source checkouts refreshed from GitHub.
	SyncedSources int
	// BuiltServices counts services rebuilt after the push.
	BuiltServices int
	// FailedBuilds counts services that failed to rebuild after the push.
	FailedBuilds int
	// RolledOutPlaygrounds counts successful automatic rollouts.
	RolledOutPlaygrounds int
}

// githubPushServiceMatch describes one service affected by a GitHub push.
type githubPushServiceMatch struct {
	summary service.Summary
	sync    bool
	build   bool
}

// HandleGitHubPush immediately syncs/builds Playgrounds tracking a GitHub branch.
func (w Worker) HandleGitHubPush(ctx context.Context, event GitHubPushEvent) (GitHubPushResult, error) {
	event, ok := normalizedGitHubPushEvent(event)
	if !ok || w.DB == nil {
		return GitHubPushResult{}, nil
	}
	playgrounds, err := w.DB.ListPlaygrounds(ctx)
	if err != nil {
		return GitHubPushResult{}, err
	}
	var result GitHubPushResult
	var errs []error
	for _, pg := range playgrounds {
		playgroundResult, err := w.handleGitHubPushPlayground(ctx, event, pg)
		result.MatchedPlaygrounds += playgroundResult.MatchedPlaygrounds
		result.SyncedSources += playgroundResult.SyncedSources
		result.BuiltServices += playgroundResult.BuiltServices
		result.FailedBuilds += playgroundResult.FailedBuilds
		result.RolledOutPlaygrounds += playgroundResult.RolledOutPlaygrounds
		if err != nil {
			errs = append(errs, err)
		}
	}
	return result, errors.Join(errs...)
}

// normalizedGitHubPushEvent validates branch and repository identity.
func normalizedGitHubPushEvent(event GitHubPushEvent) (GitHubPushEvent, bool) {
	fullName, ok := git.RepositoryFullName(event.RepositoryFullName)
	branch := strings.TrimSpace(event.Branch)
	after := strings.TrimSpace(event.After)
	if !ok || branch == "" || allZeroCommit(after) {
		return GitHubPushEvent{}, false
	}
	return GitHubPushEvent{RepositoryFullName: strings.ToLower(fullName), Branch: branch, After: after}, true
}

// allZeroCommit reports GitHub's deleted-branch SHA marker.
func allZeroCommit(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	for _, r := range raw {
		if r != '0' {
			return false
		}
	}
	return true
}

// handleGitHubPushPlayground applies one normalized push to one Playground.
func (w Worker) handleGitHubPushPlayground(ctx context.Context, event GitHubPushEvent, pg domain.Playground) (GitHubPushResult, error) {
	pushContext, err := w.githubPushPlaygroundContext(ctx, pg)
	if err != nil || !pushContext.ok {
		return GitHubPushResult{}, err
	}
	current := pushContext.playground
	matches, err := w.githubPushServiceMatches(*current.ComposeProject, event, pushContext.summaries)
	if err != nil || len(matches) == 0 {
		return GitHubPushResult{}, err
	}
	result := GitHubPushResult{MatchedPlaygrounds: 1}
	synced, syncErr := w.syncGitHubPushSources(ctx, pushContext.marquee, current, matches)
	result.SyncedSources = synced
	if syncErr != nil {
		return result, syncErr
	}
	buildResult, err := w.buildGitHubPushServices(ctx, pushContext.marquee, current, matches)
	result.BuiltServices += buildResult.BuiltServices
	result.FailedBuilds += buildResult.FailedBuilds
	if err != nil {
		return result, err
	}
	if err := w.afterGitHubPushBuild(ctx, current.ID, matches, buildResult, &result); err != nil {
		return result, err
	}
	return result, nil
}

// afterGitHubPushBuild applies post-build rollout or refresh behavior.
func (w Worker) afterGitHubPushBuild(ctx context.Context, playgroundID int64, matches []githubPushServiceMatch, buildResult GitHubPushResult, result *GitHubPushResult) error {
	if rolledOut, err := w.rolloutGitHubPushBuild(ctx, playgroundID, buildResult); err != nil {
		return err
	} else if rolledOut {
		result.RolledOutPlaygrounds++
	}
	if buildResult.BuiltServices == 0 && githubPushHasSourceMountedMatch(matches) {
		return w.refreshGitHubPushPlayground(ctx, playgroundID)
	}
	return nil
}

// githubPushPlaygroundContextResult carries one push target and service metadata.
type githubPushPlaygroundContextResult struct {
	marquee    domain.Marquee
	playground domain.Playground
	summaries  []service.Summary
	ok         bool
}

// githubPushPlaygroundContext reloads current row state and effective service metadata.
func (w Worker) githubPushPlaygroundContext(ctx context.Context, pg domain.Playground) (githubPushPlaygroundContextResult, error) {
	if w.DB == nil || pg.ID == 0 {
		return githubPushPlaygroundContextResult{playground: pg}, nil
	}
	current, err := w.DB.GetPlayground(ctx, idString(pg.ID))
	if errors.Is(err, store.ErrNotFound) {
		return githubPushPlaygroundContextResult{playground: pg}, nil
	}
	if err != nil || !shouldHandleGitHubPushPlayground(current) {
		return githubPushPlaygroundContextResult{playground: current}, err
	}
	mq, err := w.runtimeMarqueeForPlayground(ctx, current)
	if err != nil {
		return githubPushPlaygroundContextResult{playground: current}, err
	}
	return w.githubPushPlaygroundServiceContext(ctx, current, mq)
}

// githubPushPlaygroundServiceContext loads effective service metadata for a push target.
func (w Worker) githubPushPlaygroundServiceContext(ctx context.Context, current domain.Playground, mq domain.Marquee) (githubPushPlaygroundContextResult, error) {
	result := githubPushPlaygroundContextResult{marquee: mq, playground: current}
	ps, err := w.DB.GetPlayspec(ctx, idString(*current.PlayspecID))
	if err != nil {
		return result, err
	}
	ps, err = effectivePlaygroundPlayspec(ps, current)
	if err != nil {
		return result, err
	}
	summaries, err := validatedSourceServices(ps.BaseComposeYAML, "GitHub webhook")
	if err != nil {
		return result, err
	}
	result.summaries = summaries
	result.ok = true
	return result, nil
}

// shouldHandleGitHubPushPlayground selects active rows with a runtime checkout.
func shouldHandleGitHubPushPlayground(pg domain.Playground) bool {
	return pg.PlayspecID != nil &&
		pg.MarqueeID != nil &&
		pg.ComposeProject != nil &&
		(pg.Status == domain.StatusRunning ||
			pg.Status == domain.StatusHasChanges ||
			(pg.Status == domain.StatusError && playguard.SourceSyncStateReason(pg)))
}

// githubPushServiceMatches returns services affected by the pushed repo branch.
func (w Worker) githubPushServiceMatches(project string, event GitHubPushEvent, summaries []service.Summary) ([]githubPushServiceMatch, error) {
	matches := make([]githubPushServiceMatch, 0)
	for _, summary := range summaries {
		if !githubPushMatchesService(event, summary) {
			continue
		}
		match := githubPushServiceMatch{
			summary: summary,
			sync:    githubPushSyncsSource(summary),
			build:   githubPushBuildsService(summary),
		}
		if !match.sync && !match.build {
			continue
		}
		if _, ok, err := w.sourceSyncPlan(project, summary); err != nil || !ok {
			return nil, err
		}
		matches = append(matches, match)
	}
	return matches, nil
}

// githubPushMatchesService checks repository and effective branch identity.
func githubPushMatchesService(event GitHubPushEvent, summary service.Summary) bool {
	fullName, ok := git.RepositoryFullName(summary.RepoURL)
	return ok && strings.EqualFold(fullName, event.RepositoryFullName) && service.SourceBranch(summary) == event.Branch
}

// githubPushSyncsSource reports whether a service runs from a live source mount.
func githubPushSyncsSource(summary service.Summary) bool {
	return strings.TrimSpace(summary.SourceMount) != "" && !summary.Production
}

// githubPushBuildsService reports whether a service needs a new image candidate.
func githubPushBuildsService(summary service.Summary) bool {
	return buildrecord.NeedsRemoteBuild(summary)
}

// syncGitHubPushSources syncs matched services and records protected failures.
func (w Worker) syncGitHubPushSources(ctx context.Context, mq domain.Marquee, pg domain.Playground, matches []githubPushServiceMatch) (int, error) {
	plans, err := w.githubPushSourceSyncPlans(*pg.ComposeProject, matches)
	if err != nil || len(plans) == 0 {
		return 0, err
	}
	syncStatus := pg.Status
	if err := w.runSourceSyncPlans(ctx, mq, plans); err != nil {
		return len(plans), errors.Join(err, w.recordSourceSyncFailure(ctx, pg, err, syncStatus))
	}
	return len(plans), w.clearResolvedSourceSyncFailure(ctx, pg, syncStatus)
}

// githubPushSourceSyncPlans builds deduped checkout operations for matched services.
func (w Worker) githubPushSourceSyncPlans(project string, matches []githubPushServiceMatch) ([]sourceSyncPlan, error) {
	seen := map[string]bool{}
	plans := make([]sourceSyncPlan, 0, len(matches))
	for _, match := range matches {
		if !match.sync && !match.build {
			continue
		}
		plan, ok, err := w.sourceSyncPlan(project, match.summary)
		if err != nil {
			return nil, err
		}
		if !ok || seen[plan.targetPath.String()] {
			continue
		}
		seen[plan.targetPath.String()] = true
		plans = append(plans, plan)
	}
	return plans, nil
}

// buildGitHubPushServices builds matched image-backed services without rollout.
func (w Worker) buildGitHubPushServices(ctx context.Context, mq domain.Marquee, pg domain.Playground, matches []githubPushServiceMatch) (GitHubPushResult, error) {
	var result GitHubPushResult
	var errs []error
	for _, match := range matches {
		serviceResult, err := w.buildGitHubPushService(ctx, mq, pg.ID, match)
		result.BuiltServices += serviceResult.BuiltServices
		result.FailedBuilds += serviceResult.FailedBuilds
		if err != nil {
			errs = append(errs, err)
		}
	}
	return result, errors.Join(errs...)
}

// buildGitHubPushService builds one matched image-backed service.
func (w Worker) buildGitHubPushService(ctx context.Context, mq domain.Marquee, playgroundID int64, match githubPushServiceMatch) (GitHubPushResult, error) {
	if !match.build {
		return GitHubPushResult{}, nil
	}
	current, err := w.currentGitHubPushPlayground(ctx, playgroundID)
	if err != nil || !shouldHandleGitHubPushPlayground(current) {
		return GitHubPushResult{}, err
	}
	build, buildErr := w.buildService(ctx, current, mq, match.summary)
	saveErr := w.saveGitHubPushBuildResult(ctx, current, build.status, buildErr)
	if buildErr != nil {
		return GitHubPushResult{FailedBuilds: 1}, saveErr
	}
	return GitHubPushResult{BuiltServices: 1}, saveErr
}

// currentGitHubPushPlayground reloads a Playground, treating deleted rows as inactive.
func (w Worker) currentGitHubPushPlayground(ctx context.Context, playgroundID int64) (domain.Playground, error) {
	current, err := w.DB.GetPlayground(ctx, idString(playgroundID))
	if errors.Is(err, store.ErrNotFound) {
		return domain.Playground{}, nil
	}
	return current, err
}

// saveGitHubPushBuildResult merges the latest BuildRecord without changing Active.
func (w Worker) saveGitHubPushBuildResult(ctx context.Context, pg domain.Playground, status domain.PlaygroundBuildStatus, buildErr error) error {
	if status.ServiceName == "" {
		return nil
	}
	current, err := w.currentGitHubPushPlayground(ctx, pg.ID)
	if err != nil || current.ID == 0 {
		return err
	}
	current.BuildStatuses = mergeGitHubPushBuildStatus(current.BuildStatuses, status)
	if buildErr != nil {
		current = githubPushBuildFailedPlayground(current, status, buildErr)
	} else {
		current = githubPushBuildReadyPlayground(current)
	}
	_, err = w.DB.SavePlayground(ctx, current)
	return err
}

// rolloutGitHubPushBuild deploys fresh successful webhook build output when enabled.
func (w Worker) rolloutGitHubPushBuild(ctx context.Context, playgroundID int64, buildResult GitHubPushResult) (bool, error) {
	if !githubPushCanAutoRollout(w.GitHubWebhookAutoRollout, buildResult) {
		return false, nil
	}
	current, mq, ps, ok, err := w.githubPushRolloutContext(ctx, playgroundID)
	if err != nil || !ok {
		return false, err
	}
	_, err = w.DeployPlayground(ctx, current, ps, &mq)
	return err == nil, err
}

// githubPushCanAutoRollout reports whether a webhook build result is deployable.
func githubPushCanAutoRollout(enabled bool, result GitHubPushResult) bool {
	return enabled && result.BuiltServices > 0 && result.FailedBuilds == 0
}

// githubPushRolloutContext loads the latest Playground, Marquee, and Playspec.
func (w Worker) githubPushRolloutContext(ctx context.Context, playgroundID int64) (domain.Playground, domain.Marquee, domain.Playspec, bool, error) {
	current, err := w.currentGitHubPushPlayground(ctx, playgroundID)
	if err != nil || current.ID == 0 || !githubPushBuildReady(current) {
		return current, domain.Marquee{}, domain.Playspec{}, false, err
	}
	mq, err := w.runtimeMarqueeForPlayground(ctx, current)
	if err != nil {
		return current, domain.Marquee{}, domain.Playspec{}, false, err
	}
	if current.PlayspecID == nil {
		return current, mq, domain.Playspec{}, false, nil
	}
	ps, err := w.DB.GetPlayspec(ctx, idString(*current.PlayspecID))
	if err != nil {
		return current, mq, domain.Playspec{}, false, err
	}
	ps, err = effectivePlaygroundPlayspec(ps, current)
	if err != nil {
		return current, mq, ps, false, err
	}
	return current, mq, ps, true, nil
}

// githubPushBuildReady reports whether the row still has webhook-built changes.
func githubPushBuildReady(pg domain.Playground) bool {
	if pg.Status != domain.StatusHasChanges || pg.StateReason == nil {
		return false
	}
	return *pg.StateReason == webhookBuildReadyReason
}

// mergeGitHubPushBuildStatus updates Latest while preserving the deployed Active image.
func mergeGitHubPushBuildStatus(statuses []domain.PlaygroundBuildStatus, replacement domain.PlaygroundBuildStatus) []domain.PlaygroundBuildStatus {
	replacement.Active = nil
	for i := range statuses {
		if statuses[i].ServiceName != replacement.ServiceName || statuses[i].Branch != replacement.Branch {
			continue
		}
		replacement.Active = statuses[i].Active
		statuses[i] = replacement
		return statuses
	}
	return append(statuses, replacement)
}

// githubPushBuildReadyPlayground marks a successful build as deployable changes.
func githubPushBuildReadyPlayground(pg domain.Playground) domain.Playground {
	pg.Status = domain.StatusHasChanges
	pg.StateReason = new(webhookBuildReadyReason)
	pg.StateReasons = []string{webhookBuildReadyReason}
	pg.ErrorMessage = nil
	pg.BuildWarnings = nil
	pg.ErrorDetails = withoutErrorDetails(pg.ErrorDetails, "source_sync", "webhook_build")
	return pg
}

// githubPushBuildFailedPlayground records build diagnostics without marking runtime broken.
func githubPushBuildFailedPlayground(pg domain.Playground, status domain.PlaygroundBuildStatus, buildErr error) domain.Playground {
	message := buildErr.Error()
	pg.BuildWarnings = []string{"GitHub push build failed; fix the build and push again or redeploy manually"}
	pg.ErrorDetails = mergeErrorDetails(pg.ErrorDetails, "webhook_build", map[string]any{
		"category": webhookBuildFailedReason,
		"service":  status.ServiceName,
		"branch":   status.Branch,
		"message":  message,
	})
	return pg
}

// withoutErrorDetails removes resolved diagnostics and returns nil when empty.
func withoutErrorDetails(existing map[string]any, keys ...string) map[string]any {
	if len(existing) == 0 {
		return nil
	}
	out := make(map[string]any, len(existing))
	maps.Copy(out, existing)
	for _, key := range keys {
		delete(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// githubPushHasSourceMountedMatch reports whether a non-build live checkout changed.
func githubPushHasSourceMountedMatch(matches []githubPushServiceMatch) bool {
	for _, match := range matches {
		if match.sync {
			return true
		}
	}
	return false
}

// refreshGitHubPushPlayground refreshes observed runtime state after live source pulls.
func (w Worker) refreshGitHubPushPlayground(ctx context.Context, playgroundID int64) error {
	current, err := w.currentGitHubPushPlayground(ctx, playgroundID)
	if err != nil || current.ID == 0 || !playguard.ShouldRefreshRuntime(current) {
		return err
	}
	_, err = w.RefreshPlayground(ctx, current)
	return err
}

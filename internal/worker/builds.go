package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/buildrecord"
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// buildReusePollInterval controls how often a duplicate deploy checks an in-flight build.
const buildReusePollInterval = 2 * time.Second

// buildServicesResult carries build status snapshots and image references.
type buildServicesResult struct {
	statuses  []domain.PlaygroundBuildStatus
	imageRefs map[string]string
}

// buildServices builds every dynamic-image service in a Playspec.
func (w Worker) buildServices(ctx context.Context, pg domain.Playground, ps domain.Playspec, marquee domain.Marquee) (buildServicesResult, error) {
	services, err := buildrecord.Services(ps)
	if err != nil {
		return buildServicesResult{}, err
	}
	result := buildServicesResult{imageRefs: map[string]string{}}
	for _, summary := range services {
		if !buildrecord.NeedsRemoteBuild(summary) {
			continue
		}
		service, err := w.buildService(ctx, pg, marquee, summary)
		if err != nil {
			if service.status.ServiceName != "" {
				result.statuses = append(result.statuses, service.status)
				recordServiceImageRef(result.imageRefs, summary.Name, service.imageRef)
			}
			return result, err
		}
		result.statuses = append(result.statuses, service.status)
		recordServiceImageRef(result.imageRefs, summary.Name, service.imageRef)
	}
	return result, nil
}

// recordServiceImageRef stores non-empty image refs for runtime Compose injection.
func recordServiceImageRef(imageRefs map[string]string, serviceName string, imageRef string) {
	if imageRef != "" {
		imageRefs[serviceName] = imageRef
	}
}

// buildServiceResult carries one service build status and produced image ref.
type buildServiceResult struct {
	status   domain.PlaygroundBuildStatus
	imageRef string
}

// buildService creates one BuildRecord and runs the local Docker build.
func (w Worker) buildService(ctx context.Context, pg domain.Playground, marquee domain.Marquee, summary service.Summary) (buildServiceResult, error) {
	plan, err := buildrecord.NewPlan(pg, marquee, summary)
	if err != nil {
		return buildServiceResult{}, err
	}
	pending, err := w.newBuildRecordWithCommit(ctx, marquee, plan)
	if err != nil {
		if pending.record.ID == 0 {
			return buildServiceResult{}, err
		}
		return w.failBuildRecord(ctx, pending.plan.ServiceName, pending.plan.Branch, pending.record, err)
	}
	plan = pending.plan
	record, reused, err := w.reuseBuildRecord(ctx, marquee, plan, pending.record)
	if err != nil && !reused {
		return w.failBuildRecord(ctx, plan.ServiceName, plan.Branch, record, err)
	}
	if err != nil {
		return buildServiceResult{status: buildrecord.StatusFromRecord(plan.ServiceName, plan.Branch, record)}, err
	}
	if reused {
		return buildServiceResult{status: buildrecord.StatusFromRecord(plan.ServiceName, plan.Branch, record), imageRef: record.ImageRef}, nil
	}
	return w.runFreshBuildRecord(ctx, marquee, plan, record)
}

// runFreshBuildRecord performs the non-reused local image build path.
func (w Worker) runFreshBuildRecord(ctx context.Context, marquee domain.Marquee, plan buildrecord.Plan, record domain.BuildRecord) (buildServiceResult, error) {
	record, err := w.saveBuildingBuildRecord(ctx, record)
	if err != nil {
		return buildServiceResult{status: buildrecord.StatusFromRecord(plan.ServiceName, plan.Branch, record)}, err
	}
	result, err := w.runBuildImage(ctx, marquee, plan, record)
	if err != nil {
		return w.failBuildRecord(ctx, plan.ServiceName, plan.Branch, record, err)
	}
	record, err = w.saveSuccessfulBuildRecord(ctx, record, result)
	if err != nil {
		return buildServiceResult{status: buildrecord.StatusFromRecord(plan.ServiceName, plan.Branch, record)}, err
	}
	return buildServiceResult{status: buildrecord.StatusFromRecord(plan.ServiceName, plan.Branch, record), imageRef: result.ImageRef}, nil
}

// buildRecordWithPlan carries a BuildRecord and its derived build plan.
type buildRecordWithPlan struct {
	plan   buildrecord.Plan
	record domain.BuildRecord
}

// newBuildRecordWithCommit creates the pending record and attaches source commit evidence.
func (w Worker) newBuildRecordWithCommit(ctx context.Context, marquee domain.Marquee, plan buildrecord.Plan) (buildRecordWithPlan, error) {
	propID, found, err := w.buildPropID(ctx, plan.RepositoryURL)
	if err != nil {
		return buildRecordWithPlan{plan: plan}, err
	}
	if found {
		plan = plan.WithPropID(propID)
	}
	record, err := w.createBuildRecord(ctx, plan)
	if err != nil {
		return buildRecordWithPlan{plan: plan}, err
	}
	commit, err := w.Runtime.ResolveSourceCommit(ctx, marquee, plan.Project, plan.SourcePath)
	if err != nil {
		return buildRecordWithPlan{plan: plan, record: record}, err
	}
	record, err = buildrecord.AttachCommit(plan, record, commit)
	if err != nil {
		return buildRecordWithPlan{plan: plan, record: record}, err
	}
	return buildRecordWithPlan{plan: plan, record: record}, nil
}

// buildPropID returns the Prop identity for a repository-backed service when known.
func (w Worker) buildPropID(ctx context.Context, repositoryURL string) (int64, bool, error) {
	if w.DB == nil || strings.TrimSpace(repositoryURL) == "" {
		return 0, false, nil
	}
	prop, found, err := w.DB.FindPropByRepositoryURL(ctx, repositoryURL)
	if err != nil || !found {
		return 0, false, err
	}
	return prop.ID, true, nil
}

// createBuildRecord persists the pending BuildRecord for one dynamic service build.
func (w Worker) createBuildRecord(ctx context.Context, plan buildrecord.Plan) (domain.BuildRecord, error) {
	return w.DB.CreateBuildRecord(ctx, buildrecord.NewPendingRecord(plan, time.Now().UTC()))
}

// saveBuildingBuildRecord marks a BuildRecord as building after reuse checks miss.
func (w Worker) saveBuildingBuildRecord(ctx context.Context, record domain.BuildRecord) (domain.BuildRecord, error) {
	record.Status = domain.BuildStatusBuilding
	return w.DB.SaveBuildRecord(ctx, record)
}

// findReusableBuildRecord returns a verified prior image build for the same source identity.
func (w Worker) findReusableBuildRecord(ctx context.Context, marquee domain.Marquee, plan buildrecord.Plan, record domain.BuildRecord) (domain.BuildRecord, bool, error) {
	candidates, err := w.DB.ListReusableBuildRecords(ctx, store.ReusableBuildRecordQuery{
		PropID:              plan.PropID,
		PlaygroundID:        plan.PlaygroundID,
		ServiceName:         plan.ServiceName,
		Branch:              plan.Branch,
		CommitSHA:           record.CommitSHA,
		BuildIdentityDigest: record.BuildIdentityDigest,
		BuildPlatform:       record.BuildPlatform,
	})
	if err != nil {
		return domain.BuildRecord{}, false, err
	}
	if reusable, found, err := w.verifiedReusableBuildRecord(ctx, marquee, candidates, domain.BuildStatusSuccess); err != nil || found {
		return reusable, found, err
	}
	return w.waitForReusableBuildCandidates(ctx, marquee, candidates, record.ID)
}

// waitForReusableBuildCandidates waits only on equivalent in-flight records from other deploys.
func (w Worker) waitForReusableBuildCandidates(ctx context.Context, marquee domain.Marquee, candidates []domain.BuildRecord, currentID int64) (domain.BuildRecord, bool, error) {
	now := time.Now().UTC()
	for _, candidate := range candidates {
		if candidate.ID == currentID || candidate.Status != domain.BuildStatusBuilding {
			continue
		}
		if !waitableBuildingRecord(candidate, now) {
			continue
		}
		reusable, found, err := w.waitForReusableBuildRecord(ctx, marquee, candidate)
		if err != nil || found {
			return reusable, found, err
		}
	}
	return domain.BuildRecord{}, false, nil
}

// waitableBuildingRecord reports whether an in-flight build is fresh enough to wait on.
func waitableBuildingRecord(candidate domain.BuildRecord, now time.Time) bool {
	return candidate.StartedAt != nil && !candidate.StartedAt.Before(now.Add(-defaultBuildStaleTimeout))
}

// reuseBuildRecord completes the current record from prior verified image evidence when possible.
func (w Worker) reuseBuildRecord(ctx context.Context, marquee domain.Marquee, plan buildrecord.Plan, record domain.BuildRecord) (domain.BuildRecord, bool, error) {
	reusable, found, err := w.findReusableBuildRecord(ctx, marquee, plan, record)
	if err != nil {
		return record, false, err
	}
	if !found {
		return record, false, nil
	}
	record, err = w.saveReusedBuildRecord(ctx, record, reusable)
	return record, true, err
}

// verifiedReusableBuildRecord returns a matching image-backed candidate with the requested status.
func (w Worker) verifiedReusableBuildRecord(ctx context.Context, marquee domain.Marquee, candidates []domain.BuildRecord, status string) (domain.BuildRecord, bool, error) {
	for _, candidate := range candidates {
		if !reusableBuildCandidate(candidate, status) {
			continue
		}
		exists, err := w.reusableBuildImageExists(ctx, marquee, candidate)
		if err != nil {
			return domain.BuildRecord{}, false, err
		}
		if exists {
			return candidate, true, nil
		}
	}
	return domain.BuildRecord{}, false, nil
}

// reusableBuildCandidate reports whether a record has the requested reusable shape.
func reusableBuildCandidate(candidate domain.BuildRecord, status string) bool {
	return candidate.Status == status && strings.TrimSpace(candidate.ImageRef) != ""
}

// reusableBuildImageExists checks the remote image evidence for a candidate.
func (w Worker) reusableBuildImageExists(ctx context.Context, marquee domain.Marquee, candidate domain.BuildRecord) (bool, error) {
	return w.Runtime.ImageExistsForBuild(ctx, marquee, candidate.ImageRef, candidate.CommitSHA, candidate.BuildIdentityDigest)
}

// waitForReusableBuildRecord waits for an in-flight equivalent build to finish.
func (w Worker) waitForReusableBuildRecord(ctx context.Context, marquee domain.Marquee, candidate domain.BuildRecord) (domain.BuildRecord, bool, error) {
	if reusable, found, err := w.verifiedReusableBuildRecord(ctx, marquee, []domain.BuildRecord{candidate}, domain.BuildStatusBuilding); err != nil || found {
		return reusable, found, err
	}
	return w.pollReusableBuildRecord(ctx, marquee, candidate.ID)
}

// pollReusableBuildRecord polls one in-flight BuildRecord until it can be reused or ignored.
func (w Worker) pollReusableBuildRecord(ctx context.Context, marquee domain.Marquee, candidateID int64) (domain.BuildRecord, bool, error) {
	ticker := time.NewTicker(buildReusePollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(defaultBuildStaleTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return domain.BuildRecord{}, false, ctx.Err()
		case <-deadline.C:
			return domain.BuildRecord{}, false, nil
		case <-ticker.C:
			result, err := w.reusableBuildRecordPollResult(ctx, marquee, candidateID)
			if result.done || err != nil {
				return result.record, result.found, err
			}
		}
	}
}

// reusableBuildPollResult carries one reuse polling tick result.
type reusableBuildPollResult struct {
	record domain.BuildRecord
	found  bool
	done   bool
}

// reusableBuildRecordPollResult evaluates the latest state of one candidate.
func (w Worker) reusableBuildRecordPollResult(ctx context.Context, marquee domain.Marquee, candidateID int64) (reusableBuildPollResult, error) {
	current, found, err := w.reloadBuildRecord(ctx, candidateID)
	if err != nil || !found {
		return reusableBuildPollResult{done: true}, err
	}
	switch current.Status {
	case domain.BuildStatusSuccess:
		reusable, found, err := w.verifiedReusableBuildRecord(ctx, marquee, []domain.BuildRecord{current}, domain.BuildStatusSuccess)
		return reusableBuildPollResult{record: reusable, found: found, done: true}, err
	case domain.BuildStatusFailed:
		return reusableBuildPollResult{done: true}, nil
	default:
		return reusableBuildPollResult{}, nil
	}
}

// reloadBuildRecord returns false when a candidate disappeared during polling.
func (w Worker) reloadBuildRecord(ctx context.Context, id int64) (domain.BuildRecord, bool, error) {
	current, err := w.DB.GetBuildRecord(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return domain.BuildRecord{}, false, nil
	}
	if err != nil {
		return domain.BuildRecord{}, false, err
	}
	return current, true, nil
}

// saveReusedBuildRecord completes a BuildRecord from verified prior image evidence.
func (w Worker) saveReusedBuildRecord(ctx context.Context, record domain.BuildRecord, reusable domain.BuildRecord) (domain.BuildRecord, error) {
	completed := time.Now().UTC()
	record.Status = domain.BuildStatusSuccess
	record.ImageRef = reusable.ImageRef
	record.Logs = fmt.Sprintf("Reused image from build record %d", reusable.ID)
	record.Reused = true
	record.CompletedAt = &completed
	return w.DB.SaveBuildRecord(ctx, record)
}

// runBuildImage validates typed runtime paths and performs the local image build.
func (w Worker) runBuildImage(ctx context.Context, marquee domain.Marquee, plan buildrecord.Plan, record domain.BuildRecord) (runtime.BuildResult, error) {
	req, err := buildrecord.RuntimeRequest(plan, record)
	if err != nil {
		return runtime.BuildResult{}, err
	}
	return w.Runtime.BuildImage(ctx, marquee, req)
}

// saveSuccessfulBuildRecord persists final image metadata for a successful build.
func (w Worker) saveSuccessfulBuildRecord(ctx context.Context, record domain.BuildRecord, result runtime.BuildResult) (domain.BuildRecord, error) {
	completed := time.Now().UTC()
	record.CompletedAt = &completed
	record.Status = domain.BuildStatusSuccess
	record.CommitSHA = result.CommitSHA
	record.ImageRef = result.ImageRef
	record.Logs = result.Logs
	return w.DB.SaveBuildRecord(ctx, record)
}

// failBuildRecord persists build failure details while returning the cause.
func (w Worker) failBuildRecord(ctx context.Context, serviceName string, branch string, record domain.BuildRecord, cause error) (buildServiceResult, error) {
	completed := time.Now().UTC()
	msg := cause.Error()
	record.Status = domain.BuildStatusFailed
	record.ErrorMessage = &msg
	record.Logs = msg
	record.CompletedAt = &completed
	saved, err := w.DB.SaveBuildRecord(ctx, record)
	if err != nil {
		return buildServiceResult{status: buildrecord.StatusFromRecord(serviceName, branch, record)}, errors.Join(cause, err)
	}
	return buildServiceResult{status: buildrecord.StatusFromRecord(serviceName, branch, saved)}, cause
}

package buildrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	service "github.com/fibegg/fibe-distilled/internal/composefile/service"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
	"github.com/fibegg/fibe-distilled/internal/runtime"
)

// Services validates a Playspec Compose document and returns build-planning services.
func Services(ps domain.Playspec) ([]service.Summary, error) {
	validation := compose.Validate(ps.BaseComposeYAML)
	if !validation.Valid {
		return nil, fmt.Errorf("validate compose for builds: %s", strings.Join(validation.Errors, "; "))
	}
	return validation.Services, nil
}

// NeedsRemoteBuild reports whether fibe-distilled must build an image.
func NeedsRemoteBuild(summary service.Summary) bool {
	return summary.Build && summary.RepoURL != ""
}

// Plan carries immutable inputs for one service image build.
type Plan struct {
	// Project is the local Compose project name.
	Project string
	// PlaygroundID is the Playground that requested the build.
	PlaygroundID int64
	// PropID links repository-backed services to a known Prop when available.
	PropID *int64
	// ServiceName is the Compose service being built.
	ServiceName string
	// Branch is the service source branch.
	Branch string
	// SourcePath is the remote checkout path used as Docker build context.
	SourcePath string
	// RepositoryURL is the service repository URL.
	RepositoryURL string
	// Identity captures build inputs that make an image semantically distinct.
	Identity Identity
	// Platform is the Docker build platform persisted for reuse checks.
	Platform string
}

// NewPlan derives stable build inputs from Playground, Marquee, and service metadata.
func NewPlan(pg domain.Playground, marquee domain.Marquee, summary service.Summary) (Plan, error) {
	branch := service.SourceBranch(summary)
	sourceTarget, err := sourceTarget(pg, summary, branch)
	if err != nil {
		return Plan{}, err
	}
	if _, err := runtime.DeterministicImageRef(sourceTarget.project, summary.Name, "latest"); err != nil {
		return Plan{}, err
	}
	return Plan{
		Project:       sourceTarget.project,
		PlaygroundID:  pg.ID,
		ServiceName:   summary.Name,
		Branch:        branch,
		SourcePath:    sourceTarget.sourcePath,
		RepositoryURL: summary.RepoURL,
		Identity:      IdentityForService(summary),
		Platform:      valueString(marquee.BuildPlatform),
	}, nil
}

// WithPropID returns a copy of the plan linked to a known Prop.
func (p Plan) WithPropID(id int64) Plan {
	p.PropID = &id
	return p
}

// sourceTargetResult carries the remote source checkout target.
type sourceTargetResult struct {
	project    string
	sourcePath string
}

// sourceTarget returns a runtime-checked source checkout target.
func sourceTarget(pg domain.Playground, summary service.Summary, branch string) (sourceTargetResult, error) {
	if pg.ComposeProject == nil || strings.TrimSpace(*pg.ComposeProject) == "" {
		return sourceTargetResult{}, errors.New("playground compose project is required for dynamic builds")
	}
	project := strings.TrimSpace(*pg.ComposeProject)
	sourcePath := optfibe.SourceCheckoutPath(project, summary.RepoURL, branch)
	checked, err := runtime.NewRemoteCheckoutPath(project, sourcePath)
	if err != nil {
		return sourceTargetResult{}, err
	}
	return sourceTargetResult{project: project, sourcePath: checked.String()}, nil
}

// NewPendingRecord returns the initial persisted BuildRecord shape for one plan.
func NewPendingRecord(plan Plan, now time.Time) domain.BuildRecord {
	return domain.BuildRecord{
		PlaygroundID:        &plan.PlaygroundID,
		PropID:              plan.PropID,
		ServiceName:         plan.ServiceName,
		Branch:              plan.Branch,
		CommitSHA:           "",
		Status:              domain.BuildStatusPending,
		BuildDockerfilePath: plan.Identity.DockerfilePath,
		BuildTarget:         plan.Identity.BuildTarget,
		BuildArgsDigest:     plan.Identity.BuildArgsDigest,
		BuildIdentityDigest: plan.Identity.BuildIdentityDigest,
		BuildPlatform:       plan.Platform,
		BuildCacheKey:       plan.Identity.BuildIdentityDigest,
		StartedAt:           &now,
	}
}

// AttachCommit attaches deterministic commit/image evidence before reuse checks.
func AttachCommit(plan Plan, record domain.BuildRecord, commit string) (domain.BuildRecord, error) {
	record.CommitSHA = commit
	imageRef, err := runtime.DeterministicImageRef(plan.Project, plan.ServiceName, commit)
	if err != nil {
		return record, err
	}
	record.ImageRef = imageRef
	return record, nil
}

// RuntimeRequest constructs the typed runtime build request for one BuildRecord.
func RuntimeRequest(plan Plan, record domain.BuildRecord) (runtime.BuildRequest, error) {
	contextPath, err := runtime.NewRemoteCheckoutPath(plan.Project, plan.SourcePath)
	if err != nil {
		return runtime.BuildRequest{}, err
	}
	dockerfilePath, err := runtime.NewRelativeDockerfilePath(plan.Identity.DockerfilePath)
	if err != nil {
		return runtime.BuildRequest{}, err
	}
	return runtime.BuildRequest{
		Project:             plan.Project,
		PlaygroundID:        plan.PlaygroundID,
		ServiceName:         plan.ServiceName,
		ContextPath:         contextPath,
		CommitSHA:           record.CommitSHA,
		Dockerfile:          dockerfilePath,
		Target:              plan.Identity.BuildTarget,
		Platform:            domain.BuildPlatform(record.BuildPlatform),
		BuildArgs:           plan.Identity.BuildArgs,
		BuildTime:           record.CreatedAt.UTC().Format(time.RFC3339),
		BuildIdentityDigest: record.BuildIdentityDigest,
		ImageRef:            record.ImageRef,
	}, nil
}

// StatusFromRecord projects a BuildRecord into Playground status metadata.
func StatusFromRecord(serviceName string, branch string, record domain.BuildRecord) domain.PlaygroundBuildStatus {
	created := record.CreatedAt
	short := record.CommitSHA
	if len(short) > 12 {
		short = short[:12]
	}
	snapshot := &domain.PlaygroundBuildRecordSnapshot{
		ID:             record.ID,
		ServiceName:    record.ServiceName,
		Status:         record.Status,
		CommitSHA:      record.CommitSHA,
		ShortCommitSHA: short,
		ImageRef:       record.ImageRef,
		ErrorMessage:   valueString(record.ErrorMessage),
		StartedAt:      record.StartedAt,
		CompletedAt:    record.CompletedAt,
		CreatedAt:      &created,
	}
	return domain.PlaygroundBuildStatus{
		ServiceName: serviceName,
		Branch:      branch,
		Latest:      snapshot,
		Active:      snapshot,
	}
}

// Identity captures build inputs that make an image semantically distinct.
type Identity struct {
	// DockerfilePath is the repository-relative Dockerfile path.
	DockerfilePath string
	// BuildTarget is the optional Docker build target.
	BuildTarget string
	// BuildArgs are normalized Docker build arguments.
	BuildArgs []string
	// BuildArgsDigest fingerprints the build arguments alone.
	BuildArgsDigest string
	// BuildIdentityDigest fingerprints all identity-defining build inputs.
	BuildIdentityDigest string
}

// IdentityForService derives stable build digests from service metadata.
func IdentityForService(summary service.Summary) Identity {
	dockerfilePath := firstNonEmpty(summary.Dockerfile, "Dockerfile")
	buildTarget := strings.TrimSpace(summary.BuildTarget)
	buildArgs, _ := domain.ParseDockerBuildArgs(summary.BuildArgs)
	normalizedArgs := map[string]string{}
	for _, arg := range buildArgs {
		key, value, ok := strings.Cut(arg, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if ok {
			normalizedArgs[key] = strings.TrimSpace(value)
		} else {
			normalizedArgs[key] = ""
		}
	}
	buildArgsDigest := digestJSON(normalizedArgs)
	var target any
	if buildTarget != "" {
		target = buildTarget
	}
	buildIdentityDigest := digestJSON(map[string]any{
		"dockerfile_path": dockerfilePath,
		"build_target":    target,
		"build_args":      normalizedArgs,
	})
	return Identity{
		DockerfilePath:      dockerfilePath,
		BuildTarget:         buildTarget,
		BuildArgs:           buildArgs,
		BuildArgsDigest:     buildArgsDigest,
		BuildIdentityDigest: buildIdentityDigest,
	}
}

// digestJSON returns a stable SHA-256 digest for JSON-marshalable values.
func digestJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte("null")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// firstNonEmpty returns the first non-empty value.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// valueString dereferences an optional string.
func valueString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

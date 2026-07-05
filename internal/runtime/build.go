package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

// defaultBuildTimeout bounds one local Docker image build.
const defaultBuildTimeout = 45 * time.Minute

// BuildRequest describes one deterministic local image build.
type BuildRequest struct {
	// Project is the Docker Compose project and /opt/fibe directory name.
	Project string
	// PlaygroundID identifies the Playground that requested the build.
	PlaygroundID int64
	// ServiceName is the Compose service being built.
	ServiceName string
	// ContextPath is the runtime checkout directory used as Docker build context.
	ContextPath RemoteCheckoutPath
	// CommitSHA is the expected source commit, or empty to resolve HEAD.
	CommitSHA string
	// Dockerfile is the Dockerfile path relative to ContextPath.
	Dockerfile RelativeDockerfilePath
	// Target is the optional Docker build target.
	Target string
	// Platform is the optional Docker build platform.
	Platform domain.BuildPlatform
	// BuildArgs are Docker build-arg key/value strings.
	BuildArgs []string
	// BuildTime is the deterministic build-time metadata value.
	BuildTime string
	// BuildIdentityDigest identifies build-affecting inputs.
	BuildIdentityDigest string
	// ImageRef is an optional caller-supplied image tag.
	ImageRef string
}

// BuildResult reports the commit, image reference, and logs from a build.
type BuildResult struct {
	// CommitSHA is the commit actually used for the build.
	CommitSHA string
	// Logs contains combined build stdout and stderr.
	Logs string
	// ImageRef is the tag produced by the local build.
	ImageRef string
}

// BuildImage runs the centralized Marquee-local docker build command.
func (c Checker) BuildImage(ctx context.Context, marquee domain.Marquee, req BuildRequest) (BuildResult, error) {
	buildInputs, err := validateBuildRequest(req)
	if err != nil {
		return BuildResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, defaultBuildTimeout)
	defer cancel()

	commit, err := c.buildCommit(ctx, marquee, req, buildInputs.contextPath)
	if err != nil {
		return BuildResult{}, err
	}
	imageRef, err := buildImageRef(req, commit)
	if err != nil {
		return BuildResult{}, err
	}
	result, err := c.dockerBuilder().Build(ctx, marquee, DockerBuildRequest{
		Project: req.Project,
		Base:    buildInputs.playgroundBase,
		Args:    buildDockerArgs(req, buildInputs, commit, imageRef),
	})
	logs := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if err != nil {
		return BuildResult{}, fmt.Errorf("docker build failed: %w: %s", err, logs)
	}
	return BuildResult{CommitSHA: commit, Logs: logs, ImageRef: imageRef}, nil
}

// checkedBuildInputs holds validated path and platform values for a build.
type checkedBuildInputs struct {
	playgroundBase string
	contextPath    RemoteCheckoutPath
	dockerfile     RelativeDockerfilePath
	platform       domain.BuildPlatform
	buildArgs      []string
}

// validateBuildRequest checks paths before any local build command runs.
func validateBuildRequest(req BuildRequest) (checkedBuildInputs, error) {
	base, err := playgroundBase(req.Project)
	if err != nil {
		return checkedBuildInputs{}, err
	}
	if strings.TrimSpace(req.ServiceName) == "" {
		return checkedBuildInputs{}, errors.New("build service name is required")
	}
	contextPath, err := NewRemoteCheckoutPath(req.Project, req.ContextPath.String())
	if err != nil {
		return checkedBuildInputs{}, err
	}
	dockerfile, err := NewRelativeDockerfilePath(req.Dockerfile.String())
	if err != nil {
		return checkedBuildInputs{}, err
	}
	platform, err := domain.ParseBuildPlatform(req.Platform.String())
	if err != nil {
		return checkedBuildInputs{}, fmt.Errorf("build platform: %w", err)
	}
	buildArgs, err := normalizeBuildArgs(req.BuildArgs)
	if err != nil {
		return checkedBuildInputs{}, err
	}
	if err := validateBuildImageRef(req.ImageRef); err != nil {
		return checkedBuildInputs{}, err
	}
	return checkedBuildInputs{
		playgroundBase: base,
		contextPath:    contextPath,
		dockerfile:     dockerfile,
		platform:       platform,
		buildArgs:      buildArgs,
	}, nil
}

// validateBuildImageRef validates optional caller-supplied docker build tags.
func validateBuildImageRef(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := reference.ParseNormalizedNamed(raw)
	if err != nil {
		return fmt.Errorf("image ref: %w", err)
	}
	if _, ok := parsed.(reference.Canonical); ok {
		return errors.New("image ref must be a Docker image name or tag, not a digest reference")
	}
	return nil
}

// normalizeBuildArgs checks and canonicalizes build-arg entries before Docker CLI construction.
func normalizeBuildArgs(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		normalized, ok := domain.NormalizeDockerBuildArg(arg)
		if !ok {
			return nil, fmt.Errorf("build arg %q must be KEY or KEY=VALUE", arg)
		}
		out = append(out, normalized)
	}
	return out, nil
}

// buildCommit resolves the source commit when the caller did not provide one.
func (c Checker) buildCommit(ctx context.Context, marquee domain.Marquee, req BuildRequest, contextPath RemoteCheckoutPath) (string, error) {
	commit := strings.TrimSpace(req.CommitSHA)
	if commit == "" {
		resolved, err := c.ResolveSourceCommit(ctx, marquee, req.Project, contextPath.String())
		if err != nil {
			return "", err
		}
		commit = resolved
	}
	if commit == "" {
		return "", errors.New("resolve source commit failed: empty HEAD")
	}
	if !validGitCommitID(commit) {
		return "", errors.New("build commit must be a single hex git object id")
	}
	return commit, nil
}

// buildImageRef chooses the requested image ref or a deterministic local ref.
func buildImageRef(req BuildRequest, commit string) (string, error) {
	if imageRef := strings.TrimSpace(req.ImageRef); imageRef != "" {
		return imageRef, nil
	}
	return DeterministicImageRef(req.Project, req.ServiceName, commit)
}

// buildDockerArgs builds docker build CLI args for one BuildRecord.
func buildDockerArgs(req BuildRequest, inputs checkedBuildInputs, commit string, imageRef string) []string {
	args := []string{"-t", imageRef, "-f", inputs.contextPath.String() + "/" + inputs.dockerfile.String()}
	args = appendBuildArgs(args, inputs.buildArgs)
	args = appendOptionalBuildArg(args, "FIBE_BUILD_TIME", req.BuildTime)
	args = append(args, "--build-arg", "FIBE_BUILD_GIT_COMMIT_SHA="+commit)
	args = appendOptionalFlag(args, "--target", req.Target)
	args = appendOptionalFlag(args, "--platform", inputs.platform.String())
	args = append(args, buildLabels(req, commit)...)
	return append(args, inputs.contextPath.String())
}

// appendBuildArgs appends non-empty build-arg entries from Compose metadata.
func appendBuildArgs(args []string, buildArgs []string) []string {
	for _, buildArg := range buildArgs {
		if strings.TrimSpace(buildArg) != "" {
			args = append(args, "--build-arg", buildArg)
		}
	}
	return args
}

// appendOptionalBuildArg appends one build arg when a value is present.
func appendOptionalBuildArg(args []string, key string, value string) []string {
	if value = strings.TrimSpace(value); value != "" {
		return append(args, "--build-arg", key+"="+value)
	}
	return args
}

// appendOptionalFlag appends one flag/value pair when the value is present.
func appendOptionalFlag(args []string, flag string, value string) []string {
	if value = strings.TrimSpace(value); value != "" {
		return append(args, flag, value)
	}
	return args
}

// buildLabels returns Docker image labels used for build recovery and reuse.
func buildLabels(req BuildRequest, commit string) []string {
	labels := []string{
		"--label", fmt.Sprintf("fibe.playground_id=%d", req.PlaygroundID),
		"--label", "fibe.service=" + req.ServiceName,
		"--label", "fibe.build.git_commit_sha=" + commit,
		"--label", "fibe.source_commit=" + commit,
	}
	if digest := strings.TrimSpace(req.BuildIdentityDigest); digest != "" {
		labels = append(labels, "--label", "fibe.build.identity_digest="+digest)
	}
	return labels
}

// ResolveSourceCommit reads HEAD from a synced source checkout.
func (c Checker) ResolveSourceCommit(ctx context.Context, marquee domain.Marquee, project string, sourcePath string) (string, error) {
	base, err := playgroundBase(project)
	if err != nil {
		return "", err
	}
	if !optfibe.ValidRemoteCheckoutPath(sourcePath, base) {
		return "", errors.New("source commit path must be an absolute checkout path under this playground")
	}
	commit, err := c.gitRuntime().Head(ctx, marquee, project, sourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve source commit failed: %w", err)
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return "", errors.New("resolve source commit failed: empty HEAD")
	}
	if !validGitCommitID(commit) {
		return "", errors.New("resolve source commit failed: invalid HEAD")
	}
	return commit, nil
}

// ImageExistsForBuild reports whether an image matches commit and build identity.
func (c Checker) ImageExistsForBuild(ctx context.Context, marquee domain.Marquee, imageRef string, commit string, identityDigest string) (bool, error) {
	evidence := normalizedImageEvidence(imageRef, commit)
	if !evidence.ok {
		return false, nil
	}
	metadata, found, err := c.inspectImageMetadata(ctx, marquee, evidence.imageRef)
	if err != nil || !found {
		return false, err
	}
	return imageMetadataMatches(metadata, evidence.commit, identityDigest), nil
}

// imageEvidence carries normalized image cache probe inputs.
type imageEvidence struct {
	imageRef string
	commit   string
	ok       bool
}

// normalizedImageEvidence trims and validates image cache evidence.
func normalizedImageEvidence(imageRef string, commit string) imageEvidence {
	imageRef = strings.TrimSpace(imageRef)
	commit = strings.TrimSpace(commit)
	return imageEvidence{imageRef: imageRef, commit: commit, ok: imageRef != "" && commit != "" && validGitCommitID(commit)}
}

// inspectImageMetadata reads Docker image config metadata when the image exists.
func (c Checker) inspectImageMetadata(ctx context.Context, marquee domain.Marquee, imageRef string) (imageMetadata, bool, error) {
	return c.dockerRuntime().ImageMetadata(ctx, marquee, imageRef)
}

// imageMetadataMatches checks commit and optional build identity labels.
func imageMetadataMatches(metadata imageMetadata, commit string, identityDigest string) bool {
	if metadata.CommitSHA != commit {
		return false
	}
	if strings.TrimSpace(identityDigest) != "" && metadata.BuildIdentityDigest != strings.TrimSpace(identityDigest) {
		return false
	}
	return true
}

// ImageMetadata is the build identity extracted from Docker image config.
type ImageMetadata struct {
	// CommitSHA is the source commit recorded on the image.
	CommitSHA string
	// BuildIdentityDigest is the build-plan digest recorded on the image.
	BuildIdentityDigest string
}

// imageMetadata is the internal alias used by older build code.
type imageMetadata = ImageMetadata

// DeterministicImageRef builds a stable local image ref for one service commit.
func DeterministicImageRef(project string, service string, commit string) (string, error) {
	project = sanitizeImageComponent(project)
	if project == "" {
		return "", errors.New("image ref project component is empty")
	}
	service = sanitizeImageComponent(service)
	if service == "" {
		return "", errors.New("image ref service component is empty")
	}
	tag := sanitizeImageTag(commit)
	if tag == "" {
		return "", errors.New("image ref tag is empty")
	}
	imageRef := "fibe-distilled/" + project + "/" + service + ":" + tag
	if err := validateBuildImageRef(imageRef); err != nil {
		return "", err
	}
	return imageRef, nil
}

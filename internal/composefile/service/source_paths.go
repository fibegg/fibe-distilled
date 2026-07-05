package service

import (
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

// injectSourcePath rewrites build context or source mount to /opt/fibe.
func injectSourcePath(services map[string]any, summary Summary, project string) {
	target := sourcePathTarget(services, summary, project)
	if !target.ok {
		return
	}
	applySourcePath(target.serviceMap, summary, target.path)
}

// sourcePathTargetResult carries a mutable service map and remote source path.
type sourcePathTargetResult struct {
	serviceMap map[string]any
	path       string
	ok         bool
}

// sourcePathTarget returns the mutable service map and remote source path.
func sourcePathTarget(services map[string]any, summary Summary, project string) sourcePathTargetResult {
	if summary.RepoURL == "" || project == "" {
		return sourcePathTargetResult{}
	}
	raw, ok := services[summary.Name].(map[string]any)
	if !ok {
		return sourcePathTargetResult{}
	}
	return sourcePathTargetResult{serviceMap: raw, path: sourcePath(summary, project), ok: true}
}

// applySourcePath rewrites mounts or build context for a synced repository.
func applySourcePath(raw map[string]any, summary Summary, path string) {
	switch {
	case summary.SourceMount != "" && !summary.Production:
		raw["volumes"] = replaceSourceMount(raw["volumes"], summary.SourceMount, path)
		if summary.Build {
			raw["build"] = sourceBuild(path, summary)
		}
		return
	case summary.SourceMount != "":
		removeSourceMount(raw, summary.SourceMount)
	}
	if summary.Build || summary.Production {
		raw["build"] = sourceBuild(path, summary)
	}
}

// sourcePath returns the remote checkout path for one service summary.
func sourcePath(summary Summary, project string) string {
	return optfibe.SourceCheckoutPath(project, summary.RepoURL, summary.Branch)
}

// sourceBuild returns the Compose build block for a synced source checkout.
func sourceBuild(contextPath string, summary Summary) map[string]any {
	build := map[string]any{"context": contextPath}
	if summary.Dockerfile != "" {
		build["dockerfile"] = summary.Dockerfile
	}
	if summary.BuildTarget != "" {
		build["target"] = summary.BuildTarget
	}
	if args := sourceBuildArgs(summary.BuildArgs); len(args) > 0 {
		build["args"] = args
	}
	return build
}

// sourceBuildArgs returns Docker Compose list-form build args.
func sourceBuildArgs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	args, ok := domain.ParseDockerBuildArgs(raw)
	if !ok {
		return nil
	}
	return args
}

// InjectSourcePathVariables replaces FIBE_SERVICES_*_PATH volume references.
func InjectSourcePathVariables(services map[string]any, sourcePaths map[string]string) {
	if len(sourcePaths) == 0 {
		return
	}
	for _, raw := range services {
		serviceMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		volumes, ok := serviceMap["volumes"]
		if !ok || volumes == nil {
			continue
		}
		serviceMap["volumes"] = replacePathVariables(volumes, sourcePaths)
	}
}

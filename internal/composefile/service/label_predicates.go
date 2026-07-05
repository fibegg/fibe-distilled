package service

import (
	"path"
	"strings"

	fibetemplate "github.com/fibegg/fibe-distilled/internal/composefile/template"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

// validRepoRelativePath reports whether a path stays inside a repository.
func validRepoRelativePath(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	if unsafeRepoPathSyntax(raw) {
		return false
	}
	if fibetemplate.ContainsVariable(raw) {
		return true
	}
	return safeCleanRepoPath(path.Clean(raw))
}

// unsafeRepoPathSyntax rejects raw path forms that can never stay in checkout.
func unsafeRepoPathSyntax(raw string) bool {
	return strings.ContainsRune(raw, '\x00') || strings.HasPrefix(raw, "/") || pathHasParentSegment(raw)
}

// safeCleanRepoPath reports whether a cleaned path stays under checkout root.
func safeCleanRepoPath(clean string) bool {
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// validGitBranchName reports whether a branch label is safe to pass to Git.
func validGitBranchName(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	return validGitBranchShape(raw) && validGitBranchComponents(raw) && strings.IndexFunc(raw, invalidGitBranchRune) < 0
}

// validDockerBuildArgs reports whether a label contains valid Docker build args.
func validDockerBuildArgs(raw string) bool {
	parts, ok := domain.ParseDockerBuildArgs(raw)
	return ok && len(parts) > 0
}

// validGitBranchShape rejects whole-ref branch patterns Git forbids.
func validGitBranchShape(raw string) bool {
	if strings.HasPrefix(raw, "-") || strings.HasPrefix(raw, "/") || strings.HasSuffix(raw, "/") {
		return false
	}
	return !strings.Contains(raw, "//") && !strings.Contains(raw, "..") && !strings.Contains(raw, "@{") && !strings.HasSuffix(raw, ".")
}

// validGitBranchComponents rejects unsafe slash-separated branch segments.
func validGitBranchComponents(raw string) bool {
	for part := range strings.SplitSeq(raw, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// invalidGitBranchRune rejects characters Git forbids in branch refs.
func invalidGitBranchRune(r rune) bool {
	return r <= ' ' || r == 0x7f || strings.ContainsRune(`~^:?*[\`, r)
}

// validContainerPath reports whether a label is an absolute container path.
func validContainerPath(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	if strings.ContainsRune(raw, '\x00') || pathHasParentSegment(raw) {
		return false
	}
	if fibetemplate.ContainsVariable(raw) {
		return true
	}
	if !strings.HasPrefix(raw, "/") {
		return false
	}
	clean := path.Clean(raw)
	return clean != "/"
}

// pathHasParentSegment reports whether a slash path includes a parent segment.
func pathHasParentSegment(raw string) bool {
	for part := range strings.SplitSeq(raw, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

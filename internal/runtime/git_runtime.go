package runtime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/optfibe"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// GitSyncRequest describes one repository checkout/update on a Marquee.
type GitSyncRequest struct {
	// Project is the Compose project receiving the checkout.
	Project string
	// Service is the service name backed by the checkout.
	Service string
	// RepoURL is the source Git repository URL.
	RepoURL string
	// Branch is the branch to clone or update.
	Branch string
	// TargetPath is the remote checkout destination.
	TargetPath RemoteCheckoutPath
	// GitHubToken is the optional token for GitHub HTTPS clones.
	GitHubToken string
}

// GitSyncError reports a typed go-git source synchronization failure.
type GitSyncError struct {
	// Category is the stable source-sync failure class.
	Category string
	// Message is the client-visible failure text.
	Message string
	// Err is the wrapped go-git failure.
	Err error
}

// Error returns the source-sync failure message.
func (e GitSyncError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "source sync failed"
}

// Unwrap returns the wrapped go-git failure.
func (e GitSyncError) Unwrap() error {
	return e.Err
}

// GoGitRuntime implements GitRuntime with go-git and runtime filesystem sync.
type GoGitRuntime struct {
	// FS overrides the default remote filesystem.
	FS RemoteFS
}

// SyncSource runs the configured Git runtime for one checkout.
func (c Checker) SyncSource(ctx context.Context, marquee domain.Marquee, req GitSyncRequest) error {
	if _, err := playgroundBase(req.Project); err != nil {
		return err
	}
	return c.gitRuntime().Sync(ctx, marquee, req)
}

// Sync clones or updates a remote source checkout without invoking remote git.
func (r GoGitRuntime) Sync(ctx context.Context, marquee domain.Marquee, req GitSyncRequest) error {
	prepared, err := prepareGitSyncRequest(req)
	if err != nil {
		return err
	}
	targetExists, err := r.remotePathExists(ctx, marquee, prepared.TargetPath.String())
	if err != nil {
		return err
	}
	gitExists, err := r.remotePathExists(ctx, marquee, prepared.TargetPath.String()+"/.git")
	if err != nil {
		return err
	}
	if targetExists && !gitExists {
		return GitSyncError{Category: "checkout_failed", Message: "fibe_distilled_source_sync_category=checkout_failed", Err: errors.New("source path exists without .git")}
	}
	if gitExists {
		return updateGitCheckout(ctx, prepared.TargetPath.String(), prepared)
	}
	local, err := os.MkdirTemp("", "fibe-distilled-source-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(local) }()

	if err := cloneGitCheckout(local, prepared); err != nil {
		return err
	}
	return replaceRemoteDir(ctx, r.fs(), marquee, prepared.TargetPath.String(), local)
}

// prepareGitSyncRequest normalizes and validates one source sync request.
func prepareGitSyncRequest(req GitSyncRequest) (GitSyncRequest, error) {
	req.Branch = strings.TrimSpace(req.Branch)
	if req.Branch == "" {
		req.Branch = "main"
	}
	base, err := playgroundBase(req.Project)
	if err != nil {
		return req, err
	}
	if !optfibe.ValidRemoteCheckoutPath(req.TargetPath.String(), base) {
		return req, errors.New("source sync path must be an absolute checkout path under this playground")
	}
	return req, nil
}

// DirtyPaths reports source paths with dirty or unreadable checkout state.
func (r GoGitRuntime) DirtyPaths(ctx context.Context, marquee domain.Marquee, project string, sourcePaths []string) ([]string, error) {
	base, err := playgroundBase(project)
	if err != nil {
		return nil, err
	}
	var dirty []string
	for _, sourcePath := range sourcePaths {
		if !optfibe.ValidRemoteCheckoutPath(sourcePath, base) {
			return nil, fmt.Errorf("source dirty path %q must be under %s/props", sourcePath, base)
		}
		isDirty, err := r.remoteCheckoutDirty(ctx, marquee, sourcePath)
		if err != nil {
			dirty = append(dirty, sourcePath)
			continue
		}
		if isDirty {
			dirty = append(dirty, sourcePath)
		}
	}
	return dirty, nil
}

// Head resolves HEAD for a remote checkout.
func (r GoGitRuntime) Head(ctx context.Context, marquee domain.Marquee, project string, sourcePath string) (string, error) {
	base, err := playgroundBase(project)
	if err != nil {
		return "", err
	}
	if !optfibe.ValidRemoteCheckoutPath(sourcePath, base) {
		return "", errors.New("source commit path must be an absolute checkout path under this playground")
	}
	local, err := os.MkdirTemp("", "fibe-distilled-head-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(local) }()
	if err := downloadRemoteDir(ctx, r.fs(), marquee, sourcePath, local); err != nil {
		return "", err
	}
	repo, err := git.PlainOpen(local)
	if err != nil {
		return "", err
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}

// fs returns the configured runtime filesystem.
func (r GoGitRuntime) fs() RemoteFS {
	if r.FS != nil {
		return r.FS
	}
	return LocalFS{}
}

// remotePathExists reports whether a remote path exists.
func (r GoGitRuntime) remotePathExists(ctx context.Context, marquee domain.Marquee, remotePath string) (bool, error) {
	_, err := r.fs().Stat(ctx, marquee, remotePath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrRemoteFileMissing) {
		return false, nil
	}
	return false, err
}

// remoteCheckoutDirty reports whether a checkout has local changes.
func (r GoGitRuntime) remoteCheckoutDirty(ctx context.Context, marquee domain.Marquee, sourcePath string) (bool, error) {
	ready, err := r.remoteCheckoutReady(ctx, marquee, sourcePath)
	if err != nil || !ready {
		return !ready, err
	}
	return r.stagedCheckoutDirty(ctx, marquee, sourcePath)
}

// remoteCheckoutReady reports whether a source path is a readable Git checkout.
func (r GoGitRuntime) remoteCheckoutReady(ctx context.Context, marquee domain.Marquee, sourcePath string) (bool, error) {
	exists, err := r.remotePathExists(ctx, marquee, sourcePath)
	if err != nil || !exists {
		return exists, err
	}
	gitExists, err := r.remotePathExists(ctx, marquee, sourcePath+"/.git")
	if err != nil || !gitExists {
		return true, err
	}
	return true, nil
}

// stagedCheckoutDirty downloads a checkout and inspects worktree status locally.
func (r GoGitRuntime) stagedCheckoutDirty(ctx context.Context, marquee domain.Marquee, sourcePath string) (bool, error) {
	local, err := os.MkdirTemp("", "fibe-distilled-dirty-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(local) }()
	if err := downloadRemoteDir(ctx, r.fs(), marquee, sourcePath, local); err != nil {
		return true, err
	}
	if clean, ok, err := cleanGitWorktreeWithCLI(ctx, local); ok {
		return !clean, err
	}
	repo, err := git.PlainOpen(local)
	if err != nil {
		return true, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return true, err
	}
	status, err := wt.StatusWithOptions(git.StatusOptions{Strategy: git.Preload})
	if err != nil {
		return true, err
	}
	return !status.IsClean(), nil
}

// cloneGitCheckout clones a branch into a local staging directory.
func cloneGitCheckout(local string, req GitSyncRequest) error {
	_, err := git.PlainClone(local, false, &git.CloneOptions{
		URL:           gitRemoteURL(req.RepoURL),
		ReferenceName: plumbing.NewBranchReferenceName(req.Branch),
		SingleBranch:  true,
		Auth:          gitAuth(req.RepoURL, req.GitHubToken),
		Progress:      nil,
	})
	return classifyGoGitError(err)
}

// updateGitCheckout validates and updates an existing checkout in place.
func updateGitCheckout(ctx context.Context, local string, req GitSyncRequest) error {
	if err, ok := updateGitCheckoutWithCLI(ctx, local, req); ok {
		return err
	}
	return updateGitCheckoutWithGoGit(local, req)
}

// updateGitCheckoutWithGoGit updates a checkout through go-git.
func updateGitCheckoutWithGoGit(local string, req GitSyncRequest) error {
	repo, err := git.PlainOpen(local)
	if err != nil {
		return classifyGoGitError(err)
	}
	wt, err := cleanGitWorktree(repo)
	if err != nil {
		return err
	}
	if err := resetOrigin(repo, gitRemoteURL(req.RepoURL)); err != nil {
		return classifyGoGitError(err)
	}
	if err := fetchOrigin(repo, req); err != nil {
		return err
	}
	return syncGitBranch(repo, wt, req)
}

// updateGitCheckoutWithCLI updates a checkout through native Git when available.
func updateGitCheckoutWithCLI(ctx context.Context, local string, req GitSyncRequest) (error, bool) {
	if !gitCLIAvailable() {
		return nil, false
	}
	clean, _, err := cleanGitWorktreeWithCLI(ctx, local)
	if err != nil {
		return err, true
	}
	if !clean {
		return GitSyncError{Category: "dirty_work", Message: "fibe_distilled_source_sync_category=dirty_work"}, true
	}
	if err := runGitCLI(ctx, local, req, false, "remote", "set-url", "origin", gitRemoteURL(req.RepoURL)); err != nil {
		return classifyGoGitError(err), true
	}
	if err := runGitCLI(ctx, local, req, true, "fetch", "--all", "--prune"); err != nil {
		return classifyGoGitError(err), true
	}
	upstream := "origin/" + req.Branch
	if err := runGitCLI(ctx, local, req, false, "rev-parse", "--verify", upstream); err != nil {
		return GitSyncError{Category: "missing_upstream", Message: "fibe_distilled_source_sync_category=missing_upstream", Err: err}, true
	}
	if err := runGitCLI(ctx, local, req, false, "checkout", req.Branch); err != nil {
		if createErr := runGitCLI(ctx, local, req, false, "checkout", "-b", req.Branch, upstream); createErr != nil {
			return GitSyncError{Category: "checkout_failed", Message: "fibe_distilled_source_sync_category=checkout_failed", Err: errors.Join(err, createErr)}, true
		}
	}
	relation, err := gitCLIBranchRelation(ctx, local, req, upstream)
	if err != nil {
		return err, true
	}
	switch relation {
	case "ahead":
		return GitSyncError{Category: "ahead", Message: "fibe_distilled_source_sync_category=ahead"}, true
	case "diverged":
		return GitSyncError{Category: "diverged", Message: "fibe_distilled_source_sync_category=diverged"}, true
	}
	if err := runGitCLI(ctx, local, req, true, "pull", "--ff-only", "origin", req.Branch); err != nil {
		return classifyGoGitError(err), true
	}
	return nil, true
}

// cleanGitWorktreeWithCLI reports native Git cleanliness when git is available.
func cleanGitWorktreeWithCLI(ctx context.Context, local string) (bool, bool, error) {
	if !gitCLIAvailable() {
		return false, false, nil
	}
	output, err := runGitCLIOutput(ctx, local, GitSyncRequest{}, false, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return false, true, classifyGoGitError(err)
	}
	return strings.TrimSpace(output) == "", true, nil
}

// gitCLIBranchRelation classifies local/upstream history through native Git.
func gitCLIBranchRelation(ctx context.Context, local string, req GitSyncRequest, upstream string) (string, error) {
	output, err := runGitCLIOutput(ctx, local, req, false, "rev-list", "--left-right", "--count", "HEAD..."+upstream)
	if err != nil {
		return "", GitSyncError{Category: "history_unverifiable", Message: "fibe_distilled_source_sync_category=history_unverifiable", Err: err}
	}
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) != 2 {
		return "", GitSyncError{Category: "history_unverifiable", Message: "fibe_distilled_source_sync_category=history_unverifiable", Err: errors.New("unexpected rev-list count output")}
	}
	ahead, aheadErr := strconv.ParseUint(fields[0], 10, 64)
	behind, behindErr := strconv.ParseUint(fields[1], 10, 64)
	if aheadErr != nil || behindErr != nil {
		return "", GitSyncError{Category: "history_unverifiable", Message: "fibe_distilled_source_sync_category=history_unverifiable", Err: errors.Join(aheadErr, behindErr)}
	}
	switch {
	case ahead > 0 && behind > 0:
		return "diverged", nil
	case ahead > 0:
		return "ahead", nil
	default:
		return "ok", nil
	}
}

// gitCLIAvailable reports whether native Git can handle local checkout state.
func gitCLIAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// runGitCLI executes native Git and discards stdout on success.
func runGitCLI(ctx context.Context, local string, req GitSyncRequest, withAuth bool, args ...string) error {
	_, err := runGitCLIOutput(ctx, local, req, withAuth, args...)
	return err
}

// runGitCLIOutput executes native Git and returns stdout.
func runGitCLIOutput(ctx context.Context, local string, req GitSyncRequest, withAuth bool, args ...string) (string, error) {
	gitArgs := make([]string, 0, len(args)+4)
	if withAuth {
		if header := gitAuthHeader(req.RepoURL, req.GitHubToken); header != "" {
			gitArgs = append(gitArgs, "-c", "http.extraHeader="+header)
		}
	}
	gitArgs = append(gitArgs, "-C", local)
	gitArgs = append(gitArgs, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		action := "command"
		if len(args) > 0 {
			action = args[0]
		}
		return "", fmt.Errorf("git %s failed: %w: %s", action, err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

// gitAuthHeader returns the transient GitHub auth header for native Git.
func gitAuthHeader(repoURL string, token string) string {
	if !isGitHubHTTPSURL(repoURL) {
		return ""
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return "Authorization: Basic " + encoded
}

// syncGitBranch checks out, validates, and pulls the requested branch.
func syncGitBranch(repo *git.Repository, wt *git.Worktree, req GitSyncRequest) error {
	branchRef, upstreamRef, err := checkoutRequestedBranch(repo, wt, req.Branch)
	if err != nil {
		return err
	}
	if err := verifyBranchRelation(repo, branchRef, upstreamRef); err != nil {
		return err
	}
	return pullGitBranch(wt, branchRef, req)
}

// checkoutRequestedBranch checks out or creates the local requested branch.
func checkoutRequestedBranch(repo *git.Repository, wt *git.Worktree, branch string) (plumbing.ReferenceName, plumbing.ReferenceName, error) {
	branchRef := plumbing.NewBranchReferenceName(branch)
	upstreamRef := plumbing.NewRemoteReferenceName("origin", branch)
	if _, err := repo.Reference(upstreamRef, true); err != nil {
		return branchRef, upstreamRef, GitSyncError{Category: "missing_upstream", Message: "fibe_distilled_source_sync_category=missing_upstream", Err: err}
	}
	if err := checkoutBranch(repo, wt, branchRef, upstreamRef); err != nil {
		return branchRef, upstreamRef, GitSyncError{Category: "checkout_failed", Message: "fibe_distilled_source_sync_category=checkout_failed", Err: err}
	}
	return branchRef, upstreamRef, nil
}

// verifyBranchRelation rejects ahead or diverged local history.
func verifyBranchRelation(repo *git.Repository, branchRef plumbing.ReferenceName, upstreamRef plumbing.ReferenceName) error {
	state, err := branchRelation(repo, branchRef, upstreamRef)
	if err != nil {
		return GitSyncError{Category: "history_unverifiable", Message: "fibe_distilled_source_sync_category=history_unverifiable", Err: err}
	}
	switch state {
	case "ahead":
		return GitSyncError{Category: "ahead", Message: "fibe_distilled_source_sync_category=ahead"}
	case "diverged":
		return GitSyncError{Category: "diverged", Message: "fibe_distilled_source_sync_category=diverged"}
	}
	return nil
}

// pullGitBranch pulls the requested branch unless it is already current.
func pullGitBranch(wt *git.Worktree, branchRef plumbing.ReferenceName, req GitSyncRequest) error {
	err := wt.Pull(&git.PullOptions{
		RemoteName:    "origin",
		ReferenceName: branchRef,
		SingleBranch:  true,
		Auth:          gitAuth(req.RepoURL, req.GitHubToken),
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return classifyGoGitError(err)
}

// cleanGitWorktree returns a clean worktree or a typed dirty-work failure.
func cleanGitWorktree(repo *git.Repository) (*git.Worktree, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return nil, classifyGoGitError(err)
	}
	status, err := wt.StatusWithOptions(git.StatusOptions{Strategy: git.Preload})
	if err != nil {
		return nil, classifyGoGitError(err)
	}
	if !status.IsClean() {
		return nil, GitSyncError{Category: "dirty_work", Message: "fibe_distilled_source_sync_category=dirty_work"}
	}
	return wt, nil
}

// resetOrigin rewrites the origin remote URL.
func resetOrigin(repo *git.Repository, repoURL string) error {
	_ = repo.DeleteRemote("origin")
	_, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{repoURL}})
	return err
}

// fetchOrigin refreshes remote branch refs.
func fetchOrigin(repo *git.Repository, req GitSyncRequest) error {
	err := repo.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec("+refs/heads/*:refs/remotes/origin/*"),
		},
		Prune: true,
		Auth:  gitAuth(req.RepoURL, req.GitHubToken),
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return classifyGoGitError(err)
}

// checkoutBranch checks out or creates the local branch.
func checkoutBranch(repo *git.Repository, wt *git.Worktree, branchRef plumbing.ReferenceName, upstreamRef plumbing.ReferenceName) error {
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef}); err == nil {
		return nil
	}
	upstream, err := repo.Reference(upstreamRef, true)
	if err != nil {
		return err
	}
	return wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Hash: upstream.Hash(), Create: true})
}

// branchRelation compares local branch history to upstream.
func branchRelation(repo *git.Repository, branchRef plumbing.ReferenceName, upstreamRef plumbing.ReferenceName) (string, error) {
	local, err := repo.Reference(branchRef, true)
	if err != nil {
		return "", err
	}
	upstream, err := repo.Reference(upstreamRef, true)
	if err != nil {
		return "", err
	}
	if local.Hash() == upstream.Hash() {
		return "same", nil
	}
	localAncestors, err := ancestorSet(repo, local.Hash())
	if err != nil {
		return "", err
	}
	upstreamAncestors, err := ancestorSet(repo, upstream.Hash())
	if err != nil {
		return "", err
	}
	localContainsUpstream := localAncestors[upstream.Hash()]
	upstreamContainsLocal := upstreamAncestors[local.Hash()]
	switch {
	case upstreamContainsLocal:
		return "behind", nil
	case localContainsUpstream:
		return "ahead", nil
	default:
		return "diverged", nil
	}
}

// ancestorSet returns the commit ancestors reachable from a hash.
func ancestorSet(repo *git.Repository, hash plumbing.Hash) (map[plumbing.Hash]bool, error) {
	seen := map[plumbing.Hash]bool{}
	iter, err := repo.Log(&git.LogOptions{From: hash})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	err = iter.ForEach(func(commit *object.Commit) error {
		seen[commit.Hash] = true
		return nil
	})
	return seen, err
}

// gitAuth builds GitHub HTTPS authentication when needed.
func gitAuth(repoURL string, token string) *githttp.BasicAuth {
	if !isGitHubHTTPSURL(repoURL) {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: "x-access-token", Password: token}
}

// classifyGoGitError maps common go-git errors to stable categories.
func classifyGoGitError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	switch {
	case containsAny(lower, "authentication", "authorization", "could not read username", "permission denied"):
		return GitSyncError{Category: "git_auth", Message: "fibe_distilled_source_sync_category=git_auth", Err: err}
	case containsAny(lower, "repository not found", "not found"):
		return GitSyncError{Category: "repository_not_found", Message: "repository not found", Err: err}
	case containsAny(lower, "could not resolve host", "connection", "timeout"):
		return GitSyncError{Category: "network", Message: "network failure", Err: err}
	default:
		return err
	}
}

// containsAny reports whether text contains any candidate fragment.
func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

// downloadRemoteDir mirrors a remote path into a local staging directory.
func downloadRemoteDir(ctx context.Context, fsys RemoteFS, marquee domain.Marquee, remotePath string, localPath string) error {
	info, err := fsys.Stat(ctx, marquee, remotePath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return downloadRemoteFile(ctx, fsys, marquee, remotePath, localPath, info)
	}
	if err := os.MkdirAll(localPath, info.Mode().Perm()); err != nil {
		return err
	}
	return downloadRemoteDirEntries(ctx, fsys, marquee, remotePath, localPath)
}

// downloadRemoteFile mirrors one remote file into the local staging tree.
func downloadRemoteFile(ctx context.Context, fsys RemoteFS, marquee domain.Marquee, remotePath string, localPath string, info fs.FileInfo) error {
	content, err := fsys.ReadRemoteFile(ctx, marquee, remotePath)
	if err != nil {
		return err
	}
	return os.WriteFile(localPath, content, info.Mode().Perm())
}

// downloadRemoteDirEntries mirrors all child entries of a remote directory.
func downloadRemoteDirEntries(ctx context.Context, fsys RemoteFS, marquee domain.Marquee, remotePath string, localPath string) error {
	entries, err := fsys.ReadDir(ctx, marquee, remotePath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := downloadRemoteDir(ctx, fsys, marquee, path.Join(remotePath, entry.Name()), filepath.Join(localPath, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// replaceRemoteDir atomically replaces a remote checkout directory.
func replaceRemoteDir(ctx context.Context, fsys RemoteFS, marquee domain.Marquee, remotePath string, localPath string) error {
	tmp := remotePath + ".fibe-distilled-sync-tmp"
	if err := fsys.RemoveAll(ctx, marquee, tmp); err != nil {
		return err
	}
	if err := uploadLocalDir(ctx, fsys, marquee, tmp, localPath); err != nil {
		_ = fsys.RemoveAll(ctx, marquee, tmp)
		return err
	}
	if err := fsys.RemoveAll(ctx, marquee, remotePath); err != nil {
		_ = fsys.RemoveAll(ctx, marquee, tmp)
		return err
	}
	if err := fsys.MkdirAll(ctx, marquee, path.Dir(remotePath), 0o755); err != nil {
		_ = fsys.RemoveAll(ctx, marquee, tmp)
		return err
	}
	if err := fsys.Rename(ctx, marquee, tmp, remotePath); err != nil {
		_ = fsys.RemoveAll(ctx, marquee, tmp)
		return err
	}
	return nil
}

// uploadLocalDir mirrors local staged files into a remote path.
func uploadLocalDir(ctx context.Context, fsys RemoteFS, marquee domain.Marquee, remotePath string, localPath string) error {
	return filepath.WalkDir(localPath, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return uploadLocalEntry(ctx, fsys, marquee, remotePath, localPath, current, entry)
	})
}

// uploadLocalEntry mirrors one local filesystem entry to the remote tree.
func uploadLocalEntry(ctx context.Context, fsys RemoteFS, marquee domain.Marquee, remoteRoot string, localRoot string, current string, entry fs.DirEntry) error {
	target, err := remoteEntryTarget(remoteRoot, localRoot, current)
	if err != nil {
		return err
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if entry.IsDir() {
		return fsys.MkdirAll(ctx, marquee, target, info.Mode().Perm())
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return nil
	}
	// #nosec G304 -- current comes from filepath.WalkDir under the local staging root created by fibe-distilled.
	content, err := os.ReadFile(current)
	if err != nil {
		return err
	}
	return fsys.WriteRemoteFile(ctx, marquee, target, content, info.Mode().Perm())
}

// remoteEntryTarget maps a local path under localRoot to a slash remote path.
func remoteEntryTarget(remoteRoot string, localRoot string, current string) (string, error) {
	rel, err := filepath.Rel(localRoot, current)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return remoteRoot, nil
	}
	return path.Join(remoteRoot, filepath.ToSlash(rel)), nil
}

// isGitHubHTTPSURL reports whether a URL should receive GitHub token auth.
func isGitHubHTTPSURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return parsed.Scheme == "https" && (host == "github.com" || strings.HasSuffix(host, ".github.com"))
}

// gitRemoteURL strips credentials before handing a URL to go-git.
func gitRemoteURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

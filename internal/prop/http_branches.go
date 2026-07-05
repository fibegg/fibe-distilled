package prop

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/git"
)

// propsBranches returns branch metadata for a Prop.
func (h Handler) propsBranches(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadProp(w, r)
	if !ok {
		return
	}
	limit, err := branchQueryLimit(r.URL.Query())
	if err != nil {
		response.BadRequest(w, r, err.Error())
		return
	}
	branches := propBranchesForResponse(p, branchQuery(r.URL.Query()), limit)
	response.JSON(w, r, http.StatusOK, map[string]any{"branches": branches})
}

// branchQuery normalizes the optional branch autocomplete query.
func branchQuery(q url.Values) string {
	return strings.ToLower(strings.TrimSpace(q.Get("query")))
}

// propBranchesForResponse returns filtered, sorted, and limited branches.
func propBranchesForResponse(p domain.Prop, query string, limit int) []domain.PropBranch {
	branches := filterPropBranches(p.BranchRecords, query, p.DefaultBranch)
	sortPropBranches(branches)
	return limitedPropBranches(branches, limit)
}

// filterPropBranches applies autocomplete filtering and marks the default branch.
func filterPropBranches(source []domain.PropBranch, query string, defaultBranch string) []domain.PropBranch {
	branches := make([]domain.PropBranch, 0, len(source))
	for _, branch := range source {
		name := branch.Name
		if query != "" && !strings.Contains(strings.ToLower(name), query) {
			continue
		}
		branch.Default = name == defaultBranch
		branches = append(branches, branch)
	}
	return branches
}

// sortPropBranches puts the default branch first, then sorts by name.
func sortPropBranches(branches []domain.PropBranch) {
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].Default != branches[j].Default {
			return branches[i].Default
		}
		return branches[i].Name < branches[j].Name
	})
}

// limitedPropBranches caps the branch response to the requested limit.
func limitedPropBranches(branches []domain.PropBranch, limit int) []domain.PropBranch {
	if len(branches) > limit {
		return branches[:limit]
	}
	return branches
}

// branchQueryLimit parses and caps the branch autocomplete limit.
func branchQueryLimit(q url.Values) (int, error) {
	if !q.Has("limit") {
		return 20, nil
	}
	raw := strings.TrimSpace(q.Get("limit"))
	if raw == "" {
		return 0, errors.New("limit must be a positive integer")
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, errors.New("limit must be a positive integer")
	}
	if value > 50 {
		return 50, nil
	}
	return value, nil
}

// propsSync refreshes Prop metadata from its repository.
func (h Handler) propsSync(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadProp(w, r)
	if !ok {
		return
	}
	if !isGitHubURL(p.RepositoryURL) {
		response.JSON(w, r, http.StatusOK, p)
		return
	}
	updated, err := h.syncGitHubPropMetadata(r.Context(), p)
	if err != nil {
		response.Error(w, r, http.StatusUnprocessableEntity, "PROP_SYNC_FAILED", err.Error(), nil)
		return
	}
	updated, err = h.savePropSyncMetadata(r.Context(), p, updated)
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusOK, updated)
}

// savePropSyncMetadata saves sync output if the Prop repository is unchanged.
func (h Handler) savePropSyncMetadata(ctx context.Context, source domain.Prop, synced domain.Prop) (domain.Prop, error) {
	current, err := h.repo.GetProp(ctx, idString(source.ID))
	if err != nil {
		return current, err
	}
	if !git.SameRepositoryURL(current.RepositoryURL, source.RepositoryURL) {
		return current, nil
	}
	merged := current
	merged.DefaultBranch = synced.DefaultBranch
	merged.Provider = synced.Provider
	merged.Private = synced.Private
	merged.Status = synced.Status
	merged.Branches = synced.Branches
	merged.BranchRecords = synced.BranchRecords
	merged.LastSyncedAt = synced.LastSyncedAt
	return h.repo.SaveProp(ctx, merged)
}

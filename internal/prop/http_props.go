package prop

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/git"
)

// propsList returns filtered Prop resources.
func (h Handler) propsList(w http.ResponseWriter, r *http.Request) {
	writeFilteredList(w, r, h.repo.ListProps, filterProps)
}

// propsCreate creates a Git-backed Prop.
func (h Handler) propsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prop propPayload `json:"prop"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := validatePropPayload(body.Prop); err != nil {
		writePayloadErr(w, r, err)
		return
	}
	if body.Prop.RepositoryURL == "" {
		response.BadRequest(w, r, "repository_url is required")
		return
	}
	if err := h.requireRuntimeWritable(r.Context(), []string{body.Prop.RepositoryURL}); err != nil {
		writeRuntimeWritableErr(w, r, err)
		return
	}
	p := body.Prop.toDomain()
	created, err := h.repo.CreateProp(r.Context(), p)
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusCreated, created)
}

// propsGet returns one Prop by name or ID.
func (h Handler) propsGet(w http.ResponseWriter, r *http.Request) {
	writeLoadedResource(w, r, h.loadProp)
}

// propsUpdate applies Prop metadata or repository changes.
func (h Handler) propsUpdate(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadProp(w, r)
	if !ok {
		return
	}
	var body struct {
		Prop propPayload `json:"prop"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := validatePropUpdatePayload(body.Prop); err != nil {
		writePayloadErr(w, r, err)
		return
	}
	updated, err := h.savePropUpdate(r.Context(), p.ID, body.Prop)
	if err != nil {
		writePropSaveErr(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusOK, updated)
}

// validatePropUpdatePayload checks Prop PATCH payload fields.
func validatePropUpdatePayload(payload propPayload) error {
	if err := validatePropPayload(payload); err != nil {
		return err
	}
	if !payload.hasUpdateFields() {
		return badRequestError{message: "prop update requires at least one field"}
	}
	return nil
}

// savePropUpdate reloads and persists a Prop update.
func (h Handler) savePropUpdate(ctx context.Context, propID int64, payload propPayload) (domain.Prop, error) {
	current, err := h.repo.GetProp(ctx, idString(propID))
	if err != nil {
		return current, err
	}
	repositoryURL := normalizeRepositoryURLInput(payload.RepositoryURL)
	repositoryChanged := repositoryURL != "" && !git.SameRepositoryURL(repositoryURL, current.RepositoryURL)
	if repositoryChanged {
		if err := h.requireRuntimeWritable(ctx, []string{repositoryURL}); err != nil {
			return current, err
		}
	}
	payload.apply(&current)
	if repositoryChanged {
		resetPropRepositoryMetadata(&current, payload)
	}
	if err := validatePropProviderMatchesRepository(current.Provider, current.RepositoryURL); err != nil {
		return current, err
	}
	return h.repo.SaveProp(ctx, current)
}

// resetPropRepositoryMetadata clears sync-owned fields after a repository change.
func resetPropRepositoryMetadata(prop *domain.Prop, payload propPayload) {
	if !payload.fields.Has("default_branch") {
		prop.DefaultBranch = "main"
	}
	if !payload.fields.Has("provider") {
		prop.Provider = propProviderForRepository(prop.RepositoryURL)
	}
	if !payload.fields.Has("private") {
		prop.Private = false
	}
	prop.Branches = []string{prop.DefaultBranch}
	prop.BranchRecords = nil
	prop.LastSyncedAt = nil
}

// writePropSaveErr maps Prop save errors to API responses.
func writePropSaveErr(w http.ResponseWriter, r *http.Request, err error) {
	if validation, ok := errors.AsType[apiValidationError](err); ok {
		response.Error(w, r, validation.status, validation.code, validation.message, validation.details)
		return
	}
	if badRequest, ok := errors.AsType[badRequestError](err); ok {
		response.BadRequest(w, r, badRequest.message)
		return
	}
	writeStoreErr(w, r, "prop", err)
}

// validatePropPayload validates Prop scalar and unsupported fields.
func validatePropPayload(payload propPayload) error {
	if err := validatePropScalarFields(payload); err != nil {
		return err
	}
	if err := validatePropUnsupportedFields(payload); err != nil {
		return err
	}
	if payload.Provider != nil && payload.RepositoryURL != "" {
		return validatePropProviderMatchesRepository(*payload.Provider, payload.RepositoryURL)
	}
	return nil
}

// validatePropScalarFields validates basic Prop payload fields.
func validatePropScalarFields(payload propPayload) error {
	if presentBlankString(payload.fields, "repository_url", payload.RepositoryURL) {
		return badRequestError{message: "repository_url must not be blank"}
	}
	if payload.RepositoryURL != "" {
		if err := validateRuntimeRepositoryURL(payload.RepositoryURL); err != nil {
			return err
		}
	}
	if presentBlankStringPtr(payload.fields, "name", payload.Name) {
		return badRequestError{message: "name must not be blank"}
	}
	if payload.fields.Has("private") && payload.Private == nil {
		return badRequestError{message: "private must be true or false"}
	}
	if presentBlankStringPtr(payload.fields, "default_branch", payload.DefaultBranch) {
		return badRequestError{message: "default_branch must not be blank"}
	}
	if presentBlankStringPtr(payload.fields, "provider", payload.Provider) {
		return badRequestError{message: "provider must not be blank"}
	}
	return nil
}

// validatePropUnsupportedFields rejects full-Fibe-only Prop settings.
func validatePropUnsupportedFields(payload propPayload) error {
	if payload.fields.Has("credentials") {
		return apiValidationError{
			status:  http.StatusNotImplemented,
			code:    "NOT_IMPLEMENTED",
			message: "per-Prop credentials are not implemented in fibe-distilled; GitHub access uses the process GITHUB_TOKEN",
			details: map[string]any{"unsupported": []string{"field:credentials"}},
		}
	}
	if payload.Provider != nil && !supportedPropProvider(*payload.Provider) {
		provider := normalizePropProvider(*payload.Provider)
		return apiValidationError{
			status:  http.StatusNotImplemented,
			code:    "NOT_IMPLEMENTED",
			message: "this Prop provider is not implemented in fibe-distilled; use provider=github, provider=git, or omit provider",
			details: map[string]any{"unsupported": []string{"field:provider=" + provider}},
		}
	}
	return nil
}

// normalizePropProvider canonicalizes the provider selector accepted by SDK payloads.
func normalizePropProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// supportedPropProvider reports whether fibe-distilled has behavior for the provider.
func supportedPropProvider(provider string) bool {
	switch normalizePropProvider(provider) {
	case "github", "git":
		return true
	default:
		return false
	}
}

// validatePropProviderMatchesRepository rejects misleading provider metadata.
func validatePropProviderMatchesRepository(provider string, repositoryURL string) error {
	provider = normalizePropProvider(provider)
	if provider == "" || provider == propProviderForRepository(repositoryURL) {
		return nil
	}
	return badRequestError{message: "provider must match repository_url"}
}

// propProviderForRepository classifies GitHub-hosted and generic Git URLs.
func propProviderForRepository(repositoryURL string) string {
	if _, ok := git.RepositoryFullName(repositoryURL); ok {
		return "github"
	}
	return "git"
}

// propsDelete deletes an unused Prop.
func (h Handler) propsDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadProp(w, r)
	if !ok {
		return
	}
	names, err := h.repo.PlayspecsReferencingProp(r.Context(), p)
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	if len(names) > 0 {
		response.Error(w, r, http.StatusConflict, "RESOURCE_IN_USE", "cannot delete prop while playspecs reference it", map[string]any{"playspecs": names})
		return
	}
	if err := h.repo.DeleteProp(r.Context(), pathValue(r, "identifier")); err != nil {
		writeStoreErr(w, r, "prop", err)
		return
	}
	response.NoContent(w, r)
}

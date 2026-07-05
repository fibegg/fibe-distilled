package playground

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"github.com/fibegg/fibe-distilled/internal/api/request"
	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// playgroundsList returns filtered Playground resources.
func (h Handler) playgroundsList(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListPlaygrounds(r.Context())
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	filtered, err := h.filterPlaygrounds(r, items)
	if err != nil {
		if _, ok := errors.AsType[badRequestError](err); ok {
			response.BadRequest(w, r, err.Error())
			return
		}
		if validation, ok := errors.AsType[apiValidationError](err); ok {
			writePayloadErr(w, r, validation)
			return
		}
		response.ServerError(w, r, err)
		return
	}
	response.List(w, r, filtered)
}

// playgroundsCreate creates and deploys a Playground synchronously.
func (h Handler) playgroundsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Playground playgroundPayload `json:"playground"`
	}
	if !decode(w, r, &body) {
		return
	}
	pg, err := h.createAndDeployPlayground(r.Context(), body.Playground)
	if err != nil {
		writeCreatePlaygroundErr(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusCreated, pg)
}

// playgroundsGet returns one Playground by name or ID.
func (h Handler) playgroundsGet(w http.ResponseWriter, r *http.Request) {
	writeLoadedResource(w, r, h.loadPlayground)
}

// playgroundsUpdate applies metadata or runtime config changes.
func (h Handler) playgroundsUpdate(w http.ResponseWriter, r *http.Request) {
	pg, ok := h.loadPlayground(w, r)
	if !ok {
		return
	}
	payload, ok := h.decodePlaygroundUpdatePayload(w, r)
	if !ok {
		return
	}
	updated, ok := h.savePlaygroundUpdate(w, r, pg, payload)
	if !ok {
		return
	}
	response.JSON(w, r, http.StatusOK, updated)
}

// decodePlaygroundUpdatePayload decodes and validates a Playground PATCH body.
func (h Handler) decodePlaygroundUpdatePayload(w http.ResponseWriter, r *http.Request) (playgroundPayload, bool) {
	var body struct {
		Playground playgroundPayload `json:"playground"`
	}
	if !decode(w, r, &body) {
		return playgroundPayload{}, false
	}
	payload := body.Playground
	if err := validatePlaygroundPatchIntent(payload); err != nil {
		writePayloadErr(w, r, err)
		return playgroundPayload{}, false
	}
	if err := h.resolvePlaygroundReferences(r.Context(), &payload); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			response.NotFound(w, r, "playground dependency")
			return playgroundPayload{}, false
		}
		response.ServerError(w, r, err)
		return playgroundPayload{}, false
	}
	return payload, true
}

// validatePlaygroundPatchIntent checks PATCH-only Playground payload rules.
func validatePlaygroundPatchIntent(payload playgroundPayload) error {
	if payload.fields.Has("build_overrides_yaml") {
		return apiValidationError{
			status:  http.StatusNotImplemented,
			code:    "NOT_IMPLEMENTED",
			message: "build_overrides_yaml is not implemented in fibe-distilled; use compose build labels for target and args",
		}
	}
	if err := validatePlaygroundPayload(payload); err != nil {
		return err
	}
	if err := rejectReservedRunServiceOverride(payload); err != nil {
		return err
	}
	if !payload.hasUpdateFields() {
		return errors.New("playground update requires at least one field")
	}
	return nil
}

// savePlaygroundUpdate reloads then applies a PATCH payload.
func (h Handler) savePlaygroundUpdate(w http.ResponseWriter, r *http.Request, loaded domain.Playground, payload playgroundPayload) (domain.Playground, bool) {
	current, err := h.repo.GetPlayground(r.Context(), idString(loaded.ID))
	if err != nil {
		writeStoreErr(w, r, "playground", err)
		return loaded, false
	}
	before := current
	payload.apply(&current)
	if err := h.validatePlaygroundUpdate(r.Context(), current); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			response.NotFound(w, r, "playspec")
			return current, false
		}
		writePayloadErr(w, r, err)
		return current, false
	}
	markRuntimeConfigChanged(&current, before, payload)
	updated, err := h.repo.SavePlayground(r.Context(), current)
	if err != nil {
		response.ServerError(w, r, err)
		return current, false
	}
	return updated, true
}

// markRuntimeConfigChanged moves active rows to has_changes after config edits.
func markRuntimeConfigChanged(pg *domain.Playground, before domain.Playground, payload playgroundPayload) {
	if !payload.mutatesRuntimeConfig() || !playgroundRuntimeConfigChanged(before, *pg) {
		return
	}
	switch pg.Status {
	case domain.StatusPending, domain.StatusInProgress, domain.StatusRunning:
		pg.Status = domain.StatusHasChanges
		reason := "playground_config_changed"
		pg.StateReason = &reason
	}
}

// playgroundRuntimeConfigChanged compares fields that affect deployed runtime.
func playgroundRuntimeConfigChanged(before, after domain.Playground) bool {
	return !int64PtrEqual(before.PlayspecID, after.PlayspecID) ||
		!int64PtrEqual(before.MarqueeID, after.MarqueeID) ||
		!reflect.DeepEqual(before.EnvOverrides, after.EnvOverrides) ||
		!reflect.DeepEqual(before.ServiceBranches, after.ServiceBranches)
}

// playgroundsDelete destroys runtime state then deletes the row.
func (h Handler) playgroundsDelete(w http.ResponseWriter, r *http.Request) {
	pg, ok := h.loadPlayground(w, r)
	if !ok {
		return
	}
	current, ok := h.currentPlaygroundOperationClaim(w, r, pg)
	if !ok {
		return
	}
	pg = current
	if pg, ok = h.destroyPlaygroundRuntime(w, r, pg); !ok {
		return
	}
	if err := h.repo.DeletePlayground(r.Context(), idString(pg.ID)); err != nil {
		writeStoreErr(w, r, "playground", err)
		return
	}
	response.JSON(w, r, http.StatusAccepted, map[string]any{"id": pg.ID, "status": "destroying"})
}

// destroyPlaygroundRuntime destroys local Compose when a Playground has runtime.
func (h Handler) destroyPlaygroundRuntime(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	if !playgroundHasRuntimeCompose(pg) {
		return pg, true
	}
	mq, ok := h.loadPlaygroundOperationMarquee(w, r, pg, "PLAYGROUND_DESTROY_FAILED")
	if !ok {
		return pg, false
	}
	destroying, ok := h.savePlaygroundDestroying(w, r, pg)
	if !ok {
		return pg, false
	}
	if err := h.runtime.DestroyCompose(r.Context(), *mq, *destroying.ComposeProject); err != nil {
		h.writeDestroyPlaygroundFailure(w, r, destroying, err)
		return destroying, false
	}
	return destroying, true
}

// savePlaygroundDestroying persists destroying state before remote deletion.
func (h Handler) savePlaygroundDestroying(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	pg.Status = domain.StatusDestroying
	pg.ErrorMessage = nil
	pg.ErrorDetails = nil
	saved, err := h.repo.SavePlayground(r.Context(), pg)
	if err != nil {
		response.ServerError(w, r, err)
		return pg, false
	}
	return saved, true
}

// writeDestroyPlaygroundFailure saves destroy failure state before responding.
func (h Handler) writeDestroyPlaygroundFailure(w http.ResponseWriter, r *http.Request, pg domain.Playground, err error) {
	current, ok := h.currentPlaygroundOperationClaim(w, r, pg)
	if !ok {
		return
	}
	message := err.Error()
	current.Status = domain.StatusError
	current.ErrorMessage = &message
	current.ErrorDetails = map[string]any{"destroy_failure": map[string]any{"category": "compose_destroy_failed", "message": message}}
	if _, saveErr := h.repo.SavePlayground(r.Context(), current); saveErr != nil {
		response.ServerError(w, r, fmt.Errorf("%w; additionally failed to save destroy failure state: %w", err, saveErr))
		return
	}
	response.Error(w, r, http.StatusUnprocessableEntity, "PLAYGROUND_DESTROY_FAILED", message, current.ErrorDetails)
}

// playgroundsStatus returns the current Playground polling status.
func (h Handler) playgroundsStatus(w http.ResponseWriter, r *http.Request) {
	pg, ok := h.loadPlayground(w, r)
	if !ok {
		return
	}
	response.JSON(w, r, http.StatusOK, pg.StatusSnapshot(time.Now().UTC()))
}

// playgroundsStatusRefresh refreshes runtime state before returning status.
func (h Handler) playgroundsStatusRefresh(w http.ResponseWriter, r *http.Request) {
	pg, ok := h.loadPlayground(w, r)
	if !ok {
		return
	}
	status, err := h.services.RefreshPlayground(r.Context(), pg)
	if err != nil {
		response.Error(w, r, http.StatusUnprocessableEntity, "STATUS_REFRESH_FAILED", err.Error(), status.ErrorDetails)
		return
	}
	response.JSON(w, r, http.StatusOK, status)
}

// int64PtrEqual compares optional int64 values.
func int64PtrEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

// writeLoadedResource responds with a resource loaded from a route identifier.
func writeLoadedResource[T any](w http.ResponseWriter, r *http.Request, load func(http.ResponseWriter, *http.Request) (T, bool)) {
	item, ok := load(w, r)
	if !ok {
		return
	}
	response.JSON(w, r, http.StatusOK, item)
}

// loadResource loads a name-or-ID resource and writes errors.
func loadResource[T any](
	w http.ResponseWriter,
	r *http.Request,
	resource string,
	get func(context.Context, string) (T, error),
) (T, bool) {
	p, err := get(r.Context(), request.PathValue(r, "identifier"))
	if err != nil {
		writeStoreErr(w, r, resource, err)
		return p, false
	}
	return p, true
}

// loadPlayground loads a Playground using the route identifier.
func (h Handler) loadPlayground(w http.ResponseWriter, r *http.Request) (domain.Playground, bool) {
	return loadResource(w, r, "playground", h.repo.GetPlayground)
}

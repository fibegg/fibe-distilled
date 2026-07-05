package playspec

import (
	"context"
	"net/http"
	"strings"

	apilist "github.com/fibegg/fibe-distilled/internal/api/list"
	"github.com/fibegg/fibe-distilled/internal/api/request"
	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// Handler owns Playspec CRUD handlers and payload validation.
type Handler struct {
	repo Repository
}

// NewHandler constructs a Playspec handler.
func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

// List returns filtered Playspec resources.
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	apilist.WriteFiltered(w, r, h.repo.ListPlayspecs, filterPlayspecs)
}

// Create validates Compose and creates a Playspec.
func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Playspec payload `json:"playspec"`
	}
	if !request.Decode(w, r, &body) {
		return
	}
	if err := validatePayload(body.Playspec); err != nil {
		writePayloadErr(w, r, err)
		return
	}
	if strings.TrimSpace(body.Playspec.Name) == "" || strings.TrimSpace(body.Playspec.BaseComposeYAML) == "" {
		response.BadRequest(w, r, "playspec name and base_compose_yaml are required")
		return
	}
	ps, err := body.Playspec.toDomain()
	if err != nil {
		writePayloadErr(w, r, err)
		return
	}
	created, err := h.repo.CreatePlayspec(r.Context(), ps)
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusCreated, created)
}

// Get returns one Playspec by name or ID.
func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	ps, ok := h.load(w, r)
	if !ok {
		return
	}
	response.JSON(w, r, http.StatusOK, ps)
}

// Update applies Playspec metadata or Compose changes.
func (h Handler) Update(w http.ResponseWriter, r *http.Request) {
	ps, ok := h.load(w, r)
	if !ok {
		return
	}
	var body struct {
		Playspec payload `json:"playspec"`
	}
	if !request.Decode(w, r, &body) {
		return
	}
	if err := validatePayload(body.Playspec); err != nil {
		writePayloadErr(w, r, err)
		return
	}
	if !body.Playspec.hasUpdateFields() {
		response.BadRequest(w, r, "playspec update requires at least one field")
		return
	}
	updated, err := h.saveUpdate(r.Context(), ps, body.Playspec)
	if err != nil {
		writeUpdateErr(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusOK, updated)
}

// Delete deletes an unused Playspec.
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ps, ok := h.load(w, r)
	if !ok {
		return
	}
	count, err := h.repo.CountPlaygroundsForPlayspec(r.Context(), *ps.ID)
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	if count > 0 {
		response.Error(w, r, http.StatusConflict, "RESOURCE_IN_USE", "cannot delete playspec while playgrounds reference it", map[string]any{"playgrounds_count": count})
		return
	}
	if err := h.repo.DeletePlayspec(r.Context(), request.PathValue(r, "identifier")); err != nil {
		writeStoreErr(w, r, "playspec", err)
		return
	}
	response.NoContent(w, r)
}

// Services returns Playspec service metadata.
func (h Handler) Services(w http.ResponseWriter, r *http.Request) {
	ps, ok := h.load(w, r)
	if !ok {
		return
	}
	response.JSON(w, r, http.StatusOK, ps.Services)
}

// load returns one Playspec route target.
func (h Handler) load(w http.ResponseWriter, r *http.Request) (domain.Playspec, bool) {
	ps, err := h.repo.GetPlayspec(r.Context(), request.PathValue(r, "identifier"))
	if err != nil {
		writeStoreErr(w, r, "playspec", err)
		return domain.Playspec{}, false
	}
	return ps, true
}

// saveUpdate reloads, applies payload, and persists a Playspec.
func (h Handler) saveUpdate(ctx context.Context, loaded domain.Playspec, payload payload) (domain.Playspec, error) {
	if loaded.ID == nil {
		return loaded, store.ErrNotFound
	}
	current, err := h.repo.GetPlayspec(ctx, idString(*loaded.ID))
	if err != nil {
		return current, err
	}
	if err := payload.apply(&current); err != nil {
		return current, payloadError{err: err}
	}
	return h.repo.SavePlayspec(ctx, current)
}

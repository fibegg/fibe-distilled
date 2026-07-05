package playground

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	compose "github.com/fibegg/fibe-distilled/internal/composefile"
	fibetemplate "github.com/fibegg/fibe-distilled/internal/composefile/template"
	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// createAndDeployPlayground creates a Playground and deploys it synchronously.
func (h Handler) createAndDeployPlayground(ctx context.Context, payload playgroundPayload) (domain.Playground, error) {
	result, err := h.createPlaygroundRecord(ctx, payload)
	if err != nil || !result.wasCreated {
		return result.playground, err
	}
	return h.services.DeployPlayground(context.WithoutCancel(ctx), result.playground, result.playspec, result.marquee)
}

// CreatePayload is the cross-slice Playground creation request.
type CreatePayload struct {
	// Name is the requested Playground name.
	Name string
	// PlayspecID identifies the Playspec to deploy.
	PlayspecID *int64
	// MarqueeID identifies the configured Marquee.
	MarqueeID *int64
	// EnvOverrides carries runtime environment overrides.
	EnvOverrides map[string]string
	// Services carries runtime service overrides.
	Services map[string]any
}

// CreateAndDeploy creates a Playground and deploys it synchronously for neighboring slices.
func (h Handler) CreateAndDeploy(ctx context.Context, payload CreatePayload) (domain.Playground, error) {
	return h.createAndDeployPlayground(ctx, payload.toInternal())
}

// CreateAndDeployDetached creates a Playground and deploys it in background for neighboring slices.
func (h Handler) CreateAndDeployDetached(ctx context.Context, payload CreatePayload) (domain.Playground, error) {
	return h.createAndDeployPlaygroundAsync(ctx, payload.toInternal())
}

// toInternal converts cross-slice creation payloads to the Playground HTTP payload.
func (payload CreatePayload) toInternal() playgroundPayload {
	return playgroundPayload{
		Name:         payload.Name,
		PlayspecID:   payload.PlayspecID,
		MarqueeID:    payload.MarqueeID,
		EnvOverrides: payload.EnvOverrides,
		Services:     payload.Services,
	}
}

// createAndDeployPlaygroundAsync creates a Playground and deploys it in background.
func (h Handler) createAndDeployPlaygroundAsync(ctx context.Context, payload playgroundPayload) (domain.Playground, error) {
	result, err := h.createPlaygroundRecord(ctx, payload)
	if err != nil || !result.wasCreated {
		return result.playground, err
	}
	h.startPlaygroundDeploy(ctx, result.playground, result.playspec, result.marquee)
	return result.playground, nil
}

// startPlaygroundDeploy starts detached deployment logging failures.
func (h Handler) startPlaygroundDeploy(ctx context.Context, pg domain.Playground, ps domain.Playspec, mq *domain.Marquee) {
	detached := context.WithoutCancel(ctx)
	go func() {
		if _, err := h.services.DeployPlayground(detached, pg, ps, mq); err != nil {
			slog.Error("playground deploy failed", "playground", pg.Name, "playground_id", pg.ID, "error", err)
		}
	}()
}

// playgroundCreateResult carries a persisted Playground and deployment inputs.
type playgroundCreateResult struct {
	playground domain.Playground
	playspec   domain.Playspec
	marquee    *domain.Marquee
	wasCreated bool
}

// createPlaygroundRecord validates, resolves, and persists a Playground row.
func (h Handler) createPlaygroundRecord(ctx context.Context, payload playgroundPayload) (playgroundCreateResult, error) {
	prepared, err := h.preparePlaygroundCreate(ctx, payload)
	if err != nil {
		return playgroundCreateResult{}, err
	}
	payload = prepared.payload
	pg := payload.toDomain()
	if payload.fields.Has("build_overrides_yaml") {
		return playgroundCreateResult{}, apiValidationError{
			status:  http.StatusNotImplemented,
			code:    "NOT_IMPLEMENTED",
			message: "build_overrides_yaml is not implemented in fibe-distilled; use compose build labels for target and args",
		}
	}
	if err := validatePlaygroundOverridesAgainstPlayspec(pg, prepared.playspec); err != nil {
		return playgroundCreateResult{}, err
	}
	payload.applyExpiration(&pg, time.Now().UTC())
	if existing, action, err := h.compatibleExistingPlayground(ctx, pg, payload); err != nil {
		return playgroundCreateResult{}, err
	} else if action == existingPlaygroundReplay {
		return playgroundCreateResult{playground: existing, playspec: prepared.playspec, marquee: prepared.marquee}, nil
	}
	pg.Status = domain.StatusPending
	created, err := h.repo.CreatePlayground(ctx, pg)
	if err != nil {
		return h.handleCreatePlaygroundConflict(ctx, created, prepared, pg, payload, err)
	}
	return playgroundCreateResult{playground: created, playspec: prepared.playspec, marquee: prepared.marquee, wasCreated: true}, nil
}

// preparedPlaygroundCreate carries validated create payload dependencies.
type preparedPlaygroundCreate struct {
	payload  playgroundPayload
	playspec domain.Playspec
	marquee  *domain.Marquee
}

// preparePlaygroundCreate validates create payload and loads dependencies.
func (h Handler) preparePlaygroundCreate(ctx context.Context, payload playgroundPayload) (preparedPlaygroundCreate, error) {
	if err := validatePlaygroundCreateIntent(payload); err != nil {
		return preparedPlaygroundCreate{payload: payload}, err
	}
	if err := h.resolvePlaygroundReferences(ctx, &payload); err != nil {
		return preparedPlaygroundCreate{payload: payload}, err
	}
	deps, err := h.loadPlaygroundCreateDependencies(ctx, payload)
	return preparedPlaygroundCreate{payload: payload, playspec: deps.playspec, marquee: deps.marquee}, err
}

// validatePlaygroundCreateIntent checks create-only Playground payload rules.
func validatePlaygroundCreateIntent(payload playgroundPayload) error {
	if payload.Name == "" {
		return validationError("playground name is required")
	}
	if err := validatePlaygroundPayload(payload); err != nil {
		return err
	}
	if err := validatePlaygroundCreatePayload(payload); err != nil {
		return err
	}
	return rejectReservedRunServiceOverride(payload)
}

// playgroundCreateDependencies carries resources referenced by a create payload.
type playgroundCreateDependencies struct {
	playspec domain.Playspec
	marquee  *domain.Marquee
}

// loadPlaygroundCreateDependencies loads the referenced Playspec and Marquee.
func (h Handler) loadPlaygroundCreateDependencies(ctx context.Context, payload playgroundPayload) (playgroundCreateDependencies, error) {
	if payload.PlayspecID == nil {
		return playgroundCreateDependencies{}, validationError("playspec_id is required")
	}
	ps, err := h.repo.GetPlayspec(ctx, idString(*payload.PlayspecID))
	if err != nil {
		return playgroundCreateDependencies{}, err
	}
	mq, err := h.loadRequiredMarquee(ctx, payload.MarqueeID)
	return playgroundCreateDependencies{playspec: ps, marquee: mq}, err
}

// loadRequiredMarquee loads the resolved Marquee for Playground deployment.
func (h Handler) loadRequiredMarquee(ctx context.Context, id *int64) (*domain.Marquee, error) {
	if id == nil {
		return nil, validationError("marquee_id is required")
	}
	loaded, err := h.repo.GetMarquee(ctx, idString(*id))
	if err != nil {
		return nil, err
	}
	return &loaded, nil
}

// handleCreatePlaygroundConflict replays compatible duplicate creates.
func (h Handler) handleCreatePlaygroundConflict(ctx context.Context, created domain.Playground, prepared preparedPlaygroundCreate, pg domain.Playground, payload playgroundPayload, createErr error) (playgroundCreateResult, error) {
	if !store.IsUniqueConstraint(createErr) {
		return playgroundCreateResult{playground: created}, createErr
	}
	if existing, action, err := h.compatibleExistingPlayground(ctx, pg, payload); err != nil {
		return playgroundCreateResult{playground: created}, err
	} else if action == existingPlaygroundReplay {
		return playgroundCreateResult{playground: existing, playspec: prepared.playspec, marquee: prepared.marquee}, nil
	}
	return playgroundCreateResult{playground: created}, conflictError{message: "playground name already exists"}
}

// resolvePlaygroundReferences resolves Playspec and Marquee name-or-ID fields.
func (h Handler) resolvePlaygroundReferences(ctx context.Context, payload *playgroundPayload) error {
	if err := h.resolvePlaygroundPlayspec(ctx, payload); err != nil {
		return err
	}
	id, err := h.resolveConfiguredMarqueeID(ctx, payload.MarqueeID, payload.marqueeIdentifier)
	if err != nil {
		return err
	}
	payload.MarqueeID = id
	return nil
}

// resolvePlaygroundPlayspec resolves Playspec names into IDs.
func (h Handler) resolvePlaygroundPlayspec(ctx context.Context, payload *playgroundPayload) error {
	if payload.PlayspecID == nil && payload.playspecIdentifier != "" {
		ps, err := h.repo.GetPlayspec(ctx, payload.playspecIdentifier)
		if err != nil {
			return err
		}
		payload.PlayspecID = ps.ID
	}
	return nil
}

// existingPlaygroundAction describes duplicate-create handling.
type existingPlaygroundAction int

const (
	// existingPlaygroundNone means duplicate handling should not apply.
	existingPlaygroundNone existingPlaygroundAction = iota
	// existingPlaygroundReplay means return the existing compatible Playground.
	existingPlaygroundReplay
)

// compatibleExistingPlayground decides whether a duplicate create is replayable.
func (h Handler) compatibleExistingPlayground(ctx context.Context, requested domain.Playground, payload playgroundPayload) (domain.Playground, existingPlaygroundAction, error) {
	existing, err := h.repo.GetPlayground(ctx, requested.Name)
	if errors.Is(err, store.ErrNotFound) {
		return domain.Playground{}, existingPlaygroundNone, nil
	}
	if err != nil {
		return domain.Playground{}, existingPlaygroundNone, err
	}
	if !samePlaygroundCreateIntent(existing, requested, payload) {
		return domain.Playground{}, existingPlaygroundNone, conflictError{message: "playground name already exists"}
	}
	return existing, existingPlaygroundReplay, nil
}

// samePlaygroundCreateIntent compares duplicate Playground create intent.
func samePlaygroundCreateIntent(existing domain.Playground, requested domain.Playground, payload playgroundPayload) bool {
	return sameOptionalInt64(existing.PlayspecID, requested.PlayspecID) &&
		sameOptionalInt64(existing.MarqueeID, requested.MarqueeID) &&
		sameExpirationIntent(existing.ExpiresAt, requested.ExpiresAt, payload) &&
		sameStringMap(existing.EnvOverrides, requested.EnvOverrides) &&
		sameAnyMap(existing.ServiceBranches, requested.ServiceBranches)
}

// sameExpirationIntent compares duplicate-create expiration intent.
func sameExpirationIntent(existing, requested *time.Time, payload playgroundPayload) bool {
	if payload.NeverExpire != nil {
		if *payload.NeverExpire {
			return existing == nil
		}
		return existing != nil
	}
	if payload.ExpiresAt != nil {
		return sameOptionalTime(existing, requested)
	}
	return existing == nil
}

// sameOptionalInt64 compares optional integer references.
func sameOptionalInt64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// sameOptionalTime compares optional timestamps.
func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// sameStringMap treats nil and empty maps as equivalent.
func sameStringMap(a, b map[string]string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// sameAnyMap treats nil and empty maps as equivalent.
func sameAnyMap(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// rejectReservedRunServiceOverride blocks job-mode service overrides.
func rejectReservedRunServiceOverride(payload playgroundPayload) error {
	if payload.Services == nil {
		return nil
	}
	if _, ok := payload.Services["_run"]; !ok {
		return nil
	}
	return apiValidationError{
		code:    "INVALID_SERVICE_OVERRIDES",
		message: "services._run belongs to Fibe job-mode Playgrounds and is not implemented in fibe-distilled",
		details: map[string]any{"unsupported": []string{"field:services._run"}},
	}
}

// validatePlaygroundUpdate validates overrides against the current Playspec.
func (h Handler) validatePlaygroundUpdate(ctx context.Context, pg domain.Playground) error {
	if pg.PlayspecID == nil {
		return nil
	}
	ps, err := h.repo.GetPlayspec(ctx, idString(*pg.PlayspecID))
	if err != nil {
		return err
	}
	return validatePlaygroundOverridesAgainstPlayspec(pg, ps)
}

// validatePlaygroundOverridesAgainstPlayspec validates rendered override Compose.
func validatePlaygroundOverridesAgainstPlayspec(pg domain.Playground, ps domain.Playspec) error {
	patched, err := ApplyOverrides(ps.BaseComposeYAML, pg.EnvOverrides, pg.ServiceBranches)
	if err != nil {
		return apiValidationError{code: "INVALID_SERVICE_OVERRIDES", message: err.Error()}
	}
	if fibetemplate.HasUnresolvedTokens(patched) {
		return apiValidationError{
			code:    "INVALID_SERVICE_OVERRIDES",
			message: "playspec contains unresolved Fibe template variables; launch with variables through /api/launches before creating a Playground",
			details: map[string]any{"unsupported": []string{"unresolved_template_variables"}},
		}
	}
	validation := compose.Validate(patched)
	if validation.Valid {
		return nil
	}
	message := strings.Join(validation.Errors, "; ")
	return apiValidationError{
		code:    "INVALID_SERVICE_OVERRIDES",
		message: message,
		details: map[string]any{"errors": validation.Errors},
	}
}

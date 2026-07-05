package playground

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
	store "github.com/fibegg/fibe-distilled/internal/storage"
)

// playgroundsOperations handles rollout, restart, start, stop, and retry actions.
func (h Handler) playgroundsOperations(w http.ResponseWriter, r *http.Request) {
	pg, ok := h.loadPlayground(w, r)
	if !ok {
		return
	}
	var body playgroundOperationPayload
	if !decodeOptional(w, r, &body) {
		return
	}
	if rejectNestedPlaygroundOperation(w, r, body) {
		return
	}
	action, err := validatedPlaygroundOperationAction(body)
	if err != nil {
		writePayloadErr(w, r, err)
		return
	}
	if validation := validatePlaygroundOperationState(pg, action, body.force()); validation != nil {
		response.Error(w, r, validation.status, validation.code, validation.message, validation.details)
		return
	}
	if !h.validatePlaygroundOperationDependencies(w, r, pg, action) {
		return
	}
	if playgroundOperationShouldEnqueue(action, pg) {
		h.enqueuePlaygroundOperation(w, r, pg, action)
		return
	}
	updated, ok := h.applyPlaygroundOperation(w, r, pg, action)
	if !ok {
		return
	}
	h.enqueuePlaygroundOperationResult(w, r, updated)
}

// rejectNestedPlaygroundOperation blocks legacy nested operation payloads.
func rejectNestedPlaygroundOperation(w http.ResponseWriter, r *http.Request, body playgroundOperationPayload) bool {
	if !body.fields.Has("playground") {
		return false
	}
	if _, ok := body.Playground["build_overrides_yaml"]; ok {
		response.NotImplemented(w, r, "build_overrides_yaml is not implemented in fibe-distilled; use compose build labels for target and args")
		return true
	}
	response.NotImplemented(w, r, "nested playground operation payloads are not implemented in fibe-distilled; use top-level action_type and force")
	return true
}

// applyPlaygroundOperation dispatches one validated Playground action.
func (h Handler) applyPlaygroundOperation(w http.ResponseWriter, r *http.Request, pg domain.Playground, action string) (domain.Playground, bool) {
	switch action {
	case "rollout", "retry_compose":
		return h.deployPlaygroundOperation(w, r, pg)
	case "start":
		return h.startPlaygroundOperation(w, r, pg)
	case "hard_restart":
		return h.hardRestartPlayground(w, r, pg)
	case "stop":
		return h.stopPlayground(w, r, pg)
	default:
		response.BadRequest(w, r, "unsupported playground action")
		return pg, false
	}
}

// validatePlaygroundOperationDependencies rejects obvious dependency failures before enqueue.
func (h Handler) validatePlaygroundOperationDependencies(w http.ResponseWriter, r *http.Request, pg domain.Playground, action string) bool {
	if playgroundOperationCanUseRuntimeOnly(action, pg) {
		return true
	}
	if playgroundOperationNeedsPlayspec(action) && pg.PlayspecID == nil {
		response.Error(w, r, http.StatusUnprocessableEntity, "PLAYGROUND_ACTION_FAILED", "playground has no playspec to deploy", map[string]any{"dependency": "playspec"})
		return false
	}
	return true
}

// playgroundOperationNeedsPlayspec reports whether an action may redeploy from Playspec.
func playgroundOperationNeedsPlayspec(action string) bool {
	switch action {
	case "rollout", "retry_compose", "start", "hard_restart":
		return true
	default:
		return false
	}
}

// playgroundOperationCanUseRuntimeOnly reports whether an action can skip Playspec dependency.
func playgroundOperationCanUseRuntimeOnly(action string, pg domain.Playground) bool {
	switch action {
	case "start", "hard_restart":
		return playgroundHasRuntimeCompose(pg)
	default:
		return false
	}
}

// playgroundOperationShouldEnqueue reports whether an action may run a full deploy.
func playgroundOperationShouldEnqueue(action string, pg domain.Playground) bool {
	switch action {
	case "rollout", "retry_compose":
		return true
	case "start":
		return !playgroundHasRuntimeCompose(pg)
	case "hard_restart":
		return pg.PlayspecID != nil
	default:
		return false
	}
}

// enqueuePlaygroundOperation runs the real lifecycle work under the async supervisor.
func (h Handler) enqueuePlaygroundOperation(w http.ResponseWriter, r *http.Request, pg domain.Playground, action string) {
	op, err := h.services.Enqueue(r.Context(), func(ctx context.Context) (map[string]any, *domain.APIError) {
		rec := newOperationCaptureResponse()
		req := r.Clone(ctx)
		updated, ok := h.applyPlaygroundOperation(rec, req, pg, action)
		if !ok {
			return nil, operationCaptureAPIError(rec, action)
		}
		return playgroundOperationAsyncPayload(updated), nil
	})
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusAccepted, map[string]any{"request_id": op.ID, "status": "queued", "status_url": op.StatusURL})
}

// startPlaygroundOperation starts existing Compose or redeploys from Playspec.
func (h Handler) startPlaygroundOperation(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	current, ok := h.currentPlaygroundOperationClaim(w, r, pg)
	if !ok {
		return current, false
	}
	if playgroundHasRuntimeCompose(current) {
		return h.startCurrentExistingCompose(w, r, current)
	}
	return h.deployCurrentPlaygroundOperation(w, r, current)
}

// startCurrentExistingCompose starts local Compose for a current row.
func (h Handler) startCurrentExistingCompose(w http.ResponseWriter, r *http.Request, current domain.Playground) (domain.Playground, bool) {
	mq, ok := h.loadPlaygroundOperationMarquee(w, r, current, "PLAYGROUND_ACTION_FAILED")
	if !ok {
		return current, false
	}
	if err := h.runtime.StartCompose(r.Context(), *mq, *current.ComposeProject); err != nil {
		response.Error(w, r, http.StatusUnprocessableEntity, "PLAYGROUND_ACTION_FAILED", err.Error(), nil)
		return current, false
	}
	return h.savePlaygroundOperationStatus(w, r, current, domain.StatusRunning)
}

// deployPlaygroundOperation claims a row then deploys it.
func (h Handler) deployPlaygroundOperation(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	current, ok := h.currentPlaygroundOperationClaim(w, r, pg)
	if !ok {
		return current, false
	}
	return h.deployCurrentPlaygroundOperation(w, r, current)
}

// deployCurrentPlaygroundOperation deploys a claimed row from its dependencies.
func (h Handler) deployCurrentPlaygroundOperation(w http.ResponseWriter, r *http.Request, current domain.Playground) (domain.Playground, bool) {
	ps, ok := h.loadPlaygroundOperationPlayspec(w, r, current)
	if !ok {
		return current, false
	}
	mq, ok := h.loadPlaygroundOperationMarquee(w, r, current, "PLAYGROUND_ACTION_FAILED")
	if !ok {
		return current, false
	}
	deployed, err := h.services.DeployPlayground(r.Context(), current, ps, mq)
	if err != nil {
		response.Error(w, r, http.StatusUnprocessableEntity, "PLAYGROUND_ACTION_FAILED", err.Error(), deployed.ErrorDetails)
		return deployed, false
	}
	return deployed, true
}

// hardRestartPlayground downs existing Compose before redeploying or starting.
func (h Handler) hardRestartPlayground(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	pg, ok := h.downExistingComposeForRestart(w, r, pg)
	if !ok {
		return pg, false
	}
	pg, ok = h.currentPlaygroundOperationClaim(w, r, pg)
	if !ok {
		return pg, false
	}
	if pg.PlayspecID == nil {
		if playgroundHasRuntimeCompose(pg) {
			return h.startCurrentExistingCompose(w, r, pg)
		}
		response.Error(w, r, http.StatusUnprocessableEntity, "PLAYGROUND_ACTION_FAILED", "playground has no playspec to deploy", map[string]any{"dependency": "playspec"})
		return pg, false
	}
	return h.deployCurrentPlaygroundOperation(w, r, pg)
}

// downExistingComposeForRestart stops Compose before a hard restart.
func (h Handler) downExistingComposeForRestart(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	current, ok := h.currentPlaygroundOperationClaim(w, r, pg)
	if !ok {
		return current, false
	}
	if !playgroundHasRuntimeCompose(current) {
		return current, true
	}
	mq, ok := h.loadPlaygroundOperationMarquee(w, r, current, "PLAYGROUND_ACTION_FAILED")
	if !ok {
		return current, false
	}
	if err := h.runtime.DownCompose(r.Context(), *mq, *current.ComposeProject); err != nil {
		response.Error(w, r, http.StatusUnprocessableEntity, "PLAYGROUND_ACTION_FAILED", err.Error(), nil)
		return current, false
	}
	return current, true
}

// stopPlayground transitions the row and stops local Compose when present.
func (h Handler) stopPlayground(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	stopping, ok := h.savePlaygroundOperationStatus(w, r, pg, domain.StatusStopping)
	if !ok {
		return stopping, false
	}
	if playgroundHasRuntimeCompose(stopping) {
		mq, ok := h.loadPlaygroundOperationMarquee(w, r, stopping, "PLAYGROUND_ACTION_FAILED")
		if !ok {
			return stopping, false
		}
		if err := h.runtime.StopCompose(r.Context(), *mq, *stopping.ComposeProject); err != nil {
			h.writeStopPlaygroundFailure(w, r, stopping, err)
			return stopping, false
		}
	}
	return h.savePlaygroundOperationStatus(w, r, stopping, domain.StatusStopped)
}

// writeStopPlaygroundFailure saves stop failure state before responding.
func (h Handler) writeStopPlaygroundFailure(w http.ResponseWriter, r *http.Request, pg domain.Playground, err error) {
	current, ok := h.currentPlaygroundOperationClaim(w, r, pg)
	if !ok {
		return
	}
	message := err.Error()
	reason := "compose_stop_failed"
	current.Status = domain.StatusError
	current.ErrorMessage = &message
	current.StateReason = &reason
	current.StateReasons = []string{reason}
	current.ErrorDetails = map[string]any{"action_failure": map[string]any{"action": "stop", "category": reason, "message": message}}
	if _, saveErr := h.repo.SavePlayground(r.Context(), current); saveErr != nil {
		response.ServerError(w, r, fmt.Errorf("%w; additionally failed to save action failure state: %w", err, saveErr))
		return
	}
	response.Error(w, r, http.StatusUnprocessableEntity, "PLAYGROUND_ACTION_FAILED", message, current.ErrorDetails)
}

// savePlaygroundOperationStatus claims then saves a new operation status.
func (h Handler) savePlaygroundOperationStatus(w http.ResponseWriter, r *http.Request, pg domain.Playground, status string) (domain.Playground, bool) {
	current, ok := h.currentPlaygroundOperationClaim(w, r, pg)
	if !ok {
		return pg, false
	}
	current.Status = status
	saved, err := h.repo.SavePlayground(r.Context(), current)
	if err != nil {
		response.ServerError(w, r, err)
		return pg, false
	}
	return saved, true
}

// enqueuePlaygroundOperationResult returns an async-shaped result for fast sync actions.
func (h Handler) enqueuePlaygroundOperationResult(w http.ResponseWriter, r *http.Request, pg domain.Playground) {
	op, err := h.services.Enqueue(r.Context(), func(context.Context) (map[string]any, *domain.APIError) {
		return playgroundOperationAsyncPayload(pg), nil
	})
	if err != nil {
		response.ServerError(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusAccepted, map[string]any{"request_id": op.ID, "status": "queued", "status_url": op.StatusURL})
}

// currentPlaygroundOperationClaim reloads and guards against superseded actions.
func (h Handler) currentPlaygroundOperationClaim(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playground, bool) {
	current, err := h.repo.GetPlayground(r.Context(), idString(pg.ID))
	if err != nil {
		writeStoreErr(w, r, "playground", err)
		return pg, false
	}
	if current.Status != pg.Status {
		response.Error(w, r, http.StatusConflict, "INVALID_STATE", "playground operation was superseded", map[string]any{
			"current_status":  current.Status,
			"expected_status": pg.Status,
			"force_allowed":   false,
		})
		return current, false
	}
	return current, true
}

// playgroundHasRuntimeCompose reports whether local Compose actions are possible.
func playgroundHasRuntimeCompose(pg domain.Playground) bool {
	return pg.MarqueeID != nil && pg.ComposeProject != nil
}

// validatedPlaygroundOperationAction validates and resolves the SDK action_type.
func validatedPlaygroundOperationAction(body playgroundOperationPayload) (string, error) {
	action, err := validatePlaygroundOperationActionType(body)
	if err != nil {
		return "", err
	}
	if err := validatePlaygroundOperationForce(body); err != nil {
		return "", err
	}
	if !validPlaygroundOperationAction(action) {
		return "", badRequestError{message: "unsupported playground action"}
	}
	return action, nil
}

// validatePlaygroundOperationActionType checks the required action_type field.
func validatePlaygroundOperationActionType(body playgroundOperationPayload) (string, error) {
	if body.fields.Has("action") {
		return "", apiValidationError{
			status:  http.StatusNotImplemented,
			code:    "NOT_IMPLEMENTED",
			message: "playground operation field action is not implemented in fibe-distilled; use action_type",
			details: map[string]any{"unsupported": []string{"field:action"}},
		}
	}
	actionType := strings.TrimSpace(body.ActionType)
	hasActionType := body.fields.Has("action_type")
	if !hasActionType {
		return "", badRequestError{message: "action_type is required"}
	}
	if actionType == "" {
		return "", badRequestError{message: "action_type must not be blank"}
	}
	return actionType, nil
}

// validatePlaygroundOperationForce checks the optional force flag shape.
func validatePlaygroundOperationForce(body playgroundOperationPayload) error {
	if body.fields.Has("force") && body.Force == nil {
		return badRequestError{message: "force must be true or false"}
	}
	return nil
}

// validPlaygroundOperationAction checks the supported fibe-distilled action subset.
func validPlaygroundOperationAction(action string) bool {
	switch action {
	case "rollout", "retry_compose", "start", "hard_restart", "stop":
		return true
	default:
		return false
	}
}

// validatePlaygroundOperationState enforces per-action status guards.
func validatePlaygroundOperationState(pg domain.Playground, action string, force bool) *apiValidationError {
	if playgroundActionMutatesActiveCreation(action) && playgroundActiveCreation(pg.Status) {
		return &apiValidationError{
			status:  http.StatusConflict,
			code:    "INVALID_STATE",
			message: fmt.Sprintf("Cannot %s playground while deployment is already active", strings.ReplaceAll(action, "_", " ")),
			details: map[string]any{
				"current_status":   pg.Status,
				"blocked_statuses": []string{domain.StatusPending, domain.StatusInProgress},
				"force_allowed":    false,
			},
		}
	}
	if force {
		return nil
	}
	rule, ok := playgroundOperationRules[action]
	if !ok || playgroundStatusIn(pg.Status, rule.allowedStatuses...) {
		return nil
	}
	return invalidPlaygroundOperationState(pg.Status, rule.message, rule.detailAllowedStatuses)
}

// playgroundOperationRule defines allowed statuses for an action.
type playgroundOperationRule struct {
	message               string
	allowedStatuses       []string
	detailAllowedStatuses []string
}

// restartablePlaygroundStatuses are statuses that can be redeployed.
var restartablePlaygroundStatuses = []string{domain.StatusRunning, domain.StatusHasChanges, domain.StatusError}

// stopAllowedPlaygroundStatuses are statuses that can accept stop.
var stopAllowedPlaygroundStatuses = []string{domain.StatusPending, domain.StatusInProgress, domain.StatusRunning, domain.StatusHasChanges, domain.StatusError}

// playgroundOperationRules maps actions to status validation rules.
var playgroundOperationRules = map[string]playgroundOperationRule{
	"rollout":       {message: "Cannot rollout playground from current status", allowedStatuses: restartablePlaygroundStatuses, detailAllowedStatuses: restartablePlaygroundStatuses},
	"retry_compose": {message: "Cannot retry compose playground from current status", allowedStatuses: restartablePlaygroundStatuses, detailAllowedStatuses: restartablePlaygroundStatuses},
	"hard_restart":  {message: "Cannot hard restart playground from current status", allowedStatuses: restartablePlaygroundStatuses, detailAllowedStatuses: restartablePlaygroundStatuses},
	"stop":          {message: "Cannot stop playground from current status", allowedStatuses: stopAllowedPlaygroundStatuses},
	"start":         {message: "Cannot start playground from current status", allowedStatuses: []string{domain.StatusStopped}, detailAllowedStatuses: []string{domain.StatusStopped}},
}

// invalidPlaygroundOperationState builds the SDK invalid-state error.
func invalidPlaygroundOperationState(status, message string, allowedStatuses []string) *apiValidationError {
	details := map[string]any{"current_status": status, "force_allowed": true}
	if len(allowedStatuses) > 0 {
		details["allowed_statuses"] = allowedStatuses
	}
	return &apiValidationError{
		status:  http.StatusUnprocessableEntity,
		code:    "INVALID_STATE",
		message: message,
		details: details,
	}
}

// playgroundActionMutatesActiveCreation reports actions blocked during deploy.
func playgroundActionMutatesActiveCreation(action string) bool {
	switch action {
	case "rollout", "hard_restart", "start", "retry_compose":
		return true
	default:
		return false
	}
}

// playgroundActiveCreation reports pending/in-progress creation states.
func playgroundActiveCreation(status string) bool {
	return status == domain.StatusPending || status == domain.StatusInProgress
}

// playgroundStatusIn reports whether a status is in a small set.
func playgroundStatusIn(status string, allowed ...string) bool {
	return slices.Contains(allowed, status)
}

// loadPlaygroundOperationMarquee loads the Marquee dependency for an action.
func (h Handler) loadPlaygroundOperationMarquee(w http.ResponseWriter, r *http.Request, pg domain.Playground, code string) (*domain.Marquee, bool) {
	if pg.MarqueeID == nil {
		response.Error(w, r, http.StatusUnprocessableEntity, code, "playground has no marquee to operate", map[string]any{"dependency": "marquee"})
		return nil, false
	}
	loaded, found, err := h.repo.GetRuntimeMarquee(r.Context())
	if err != nil {
		writePlaygroundOperationDependencyErr(w, r, code, "marquee", *pg.MarqueeID, err)
		return nil, false
	}
	if !found {
		writePlaygroundOperationDependencyErr(w, r, code, "marquee", *pg.MarqueeID, store.ErrNotFound)
		return nil, false
	}
	return &loaded, true
}

// loadPlaygroundOperationPlayspec loads the Playspec dependency for redeploy.
func (h Handler) loadPlaygroundOperationPlayspec(w http.ResponseWriter, r *http.Request, pg domain.Playground) (domain.Playspec, bool) {
	if pg.PlayspecID == nil {
		response.Error(w, r, http.StatusUnprocessableEntity, "PLAYGROUND_ACTION_FAILED", "playground has no playspec to deploy", map[string]any{"dependency": "playspec"})
		return domain.Playspec{}, false
	}
	loaded, err := h.repo.GetPlayspec(r.Context(), idString(*pg.PlayspecID))
	if err != nil {
		writePlaygroundOperationDependencyErr(w, r, "PLAYGROUND_ACTION_FAILED", "playspec", *pg.PlayspecID, err)
		return domain.Playspec{}, false
	}
	return loaded, true
}

// writePlaygroundOperationDependencyErr maps missing action dependencies.
func writePlaygroundOperationDependencyErr(w http.ResponseWriter, r *http.Request, code string, kind string, id int64, err error) {
	if errors.Is(err, store.ErrNotFound) {
		response.Error(w, r, http.StatusUnprocessableEntity, code, fmt.Sprintf("playground references missing %s %d", kind, id), map[string]any{"dependency": kind, "id": id})
		return
	}
	response.ServerError(w, r, err)
}

// playgroundOperationAsyncPayload is the terminal success payload decoded by SDK action calls.
func playgroundOperationAsyncPayload(pg domain.Playground) map[string]any {
	return map[string]any{
		"id":                pg.ID,
		"name":              pg.Name,
		"playground_status": pg.Status,
		"completed_at":      time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// operationCaptureResponse captures operation errors written by existing sync helpers.
type operationCaptureResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

// newOperationCaptureResponse constructs an in-memory response writer.
func newOperationCaptureResponse() *operationCaptureResponse {
	return &operationCaptureResponse{header: make(http.Header)}
}

// Header returns captured response headers.
func (w *operationCaptureResponse) Header() http.Header {
	return w.header
}

// Write captures a response body.
func (w *operationCaptureResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

// WriteHeader captures a response status.
func (w *operationCaptureResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

// operationCaptureAPIError converts a captured HTTP error into an async API error.
func operationCaptureAPIError(rec *operationCaptureResponse, action string) *domain.APIError {
	apiErr := &domain.APIError{
		Code:    "PLAYGROUND_ACTION_FAILED",
		Message: fmt.Sprintf("playground %s action failed", strings.ReplaceAll(action, "_", " ")),
	}
	if rec == nil || rec.body.Len() == 0 {
		return apiErr
	}
	var decoded struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &decoded); err != nil {
		apiErr.Details = map[string]any{"response_body": rec.body.String()}
		return apiErr
	}
	if decoded.Error.Code != "" {
		apiErr.Code = decoded.Error.Code
	}
	if decoded.Error.Message != "" {
		apiErr.Message = decoded.Error.Message
	}
	apiErr.Details = decoded.Error.Details
	return apiErr
}

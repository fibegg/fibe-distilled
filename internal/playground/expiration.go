package playground

import (
	"net/http"
	"time"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	"github.com/fibegg/fibe-distilled/internal/domain"
)

// maxExpirationDurationHours is the largest hour value that fits time.Duration.
const maxExpirationDurationHours = int64(1<<63-1) / int64(time.Hour)

// playgroundsExpiration extends a Playground expiration time.
func (h Handler) playgroundsExpiration(w http.ResponseWriter, r *http.Request) {
	pg, ok := h.loadPlayground(w, r)
	if !ok {
		return
	}
	var body playgroundExpirationPayload
	if !decodeOptional(w, r, &body) {
		return
	}
	duration, err := playgroundExpirationDuration(body)
	if err != nil {
		writePayloadErr(w, r, err)
		return
	}
	extended := h.extendPlaygroundExpiration(w, r, pg, duration)
	if !extended.ok {
		return
	}
	timeRemaining := 0.0
	if extended.playground.TimeRemaining != nil {
		timeRemaining = *extended.playground.TimeRemaining
	}
	response.JSON(w, r, http.StatusOK, map[string]any{
		"id":             pg.ID,
		"expires_at":     extended.expiresAt,
		"time_remaining": timeRemaining,
	})
}

// playgroundExpirationExtension carries a saved expiration update outcome.
type playgroundExpirationExtension struct {
	playground domain.Playground
	expiresAt  time.Time
	ok         bool
}

// extendPlaygroundExpiration reloads and extends a non-terminal Playground.
func (h Handler) extendPlaygroundExpiration(w http.ResponseWriter, r *http.Request, loaded domain.Playground, duration time.Duration) playgroundExpirationExtension {
	pg, err := h.repo.GetPlayground(r.Context(), idString(loaded.ID))
	if err != nil {
		writeStoreErr(w, r, "playground", err)
		return playgroundExpirationExtension{playground: loaded}
	}
	if !canExtendExpiration(pg.Status) {
		response.Error(w, r, http.StatusUnprocessableEntity, "INVALID_STATE", "Cannot extend playground expiration from current status", map[string]any{
			"current_status":   pg.Status,
			"blocked_statuses": []string{domain.StatusCompleted, domain.StatusDestroying, domain.StatusStopping, domain.StatusStopped},
		})
		return playgroundExpirationExtension{playground: pg}
	}
	now := time.Now().UTC()
	base := now
	if pg.ExpiresAt != nil && pg.ExpiresAt.After(base) {
		base = *pg.ExpiresAt
	}
	expires := base.Add(duration)
	pg.ExpiresAt = &expires
	saved, err := h.repo.SavePlayground(r.Context(), pg)
	if err != nil {
		response.ServerError(w, r, err)
		return playgroundExpirationExtension{playground: pg}
	}
	return playgroundExpirationExtension{playground: saved, expiresAt: expires, ok: true}
}

// playgroundExpirationDuration returns the requested or default extension.
func playgroundExpirationDuration(body playgroundExpirationPayload) (time.Duration, error) {
	if !body.fields.Has("duration_hours") {
		return defaultPlaygroundTTL(), nil
	}
	if body.DurationHours == nil {
		return 0, badRequestError{message: "duration_hours must be positive and within range"}
	}
	duration, ok := expirationDuration(*body.DurationHours)
	if !ok {
		return 0, badRequestError{message: "duration_hours must be positive and within range"}
	}
	return duration, nil
}

// expirationDuration converts a positive hour count without overflowing.
func expirationDuration(hours int) (time.Duration, bool) {
	if hours <= 0 || int64(hours) > maxExpirationDurationHours {
		return 0, false
	}
	return time.Duration(hours) * time.Hour, true
}

// canExtendExpiration reports statuses that may receive a new expiration.
func canExtendExpiration(status string) bool {
	return status != domain.StatusCompleted && status != domain.StatusDestroying && status != domain.StatusStopping && status != domain.StatusStopped
}

// defaultPlaygroundTTL returns the default expiration extension duration.
func defaultPlaygroundTTL() time.Duration {
	return 8 * time.Hour
}

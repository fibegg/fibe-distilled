package storage

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// ListPlaygrounds returns all Playgrounds ordered by insertion.
func (s *DB) ListPlaygrounds(ctx context.Context) ([]domain.Playground, error) {
	return queryRows(ctx, s.db, playgroundSelectSQL()+` ORDER BY pg.id`, scanPlayground)
}

// CreatePlayground inserts a Playground and assigns runtime defaults.
func (s *DB) CreatePlayground(ctx context.Context, p domain.Playground) (domain.Playground, error) {
	now := time.Now().UTC()
	p = playgroundForInsert(p, now)
	encoded, err := encodePlaygroundJSON(p)
	if err != nil {
		return p, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO playgrounds (name,status,playspec_id,marquee_id,compose_project,root_domain,routing_scheme,internal_password,env_overrides_json,service_branches_json,generated_compose_yaml,services_json,service_urls_json,build_statuses_json,creation_steps_json,expires_at,last_applied_at,error_message,state_reason,state_reasons_json,build_warnings_json,error_details_json,playguard_repair_reason,playguard_repair_lock_until,needs_recreation,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Status, nullableInt64(p.PlayspecID), nullableInt64(p.MarqueeID), nullableString(p.ComposeProject), nullableString(p.RootDomain), nullableString(p.RoutingScheme), nullableString(p.InternalPassword), encoded.Env, encoded.Branches, p.GeneratedComposeYAML, encoded.Services, encoded.URLs, encoded.Builds, encoded.Steps, nullableTime(p.ExpiresAt), nullableTime(p.LastAppliedAt), nullableString(p.ErrorMessage), nullableString(p.StateReason), encoded.StateReasons, encoded.BuildWarnings, encoded.ErrorDetails, nullableString(p.PlayguardRepairReason), nullableTime(p.PlayguardRepairLockUntil), boolValue(p.NeedsRecreation), encodeTime(now), encodeTime(now))
	if err != nil {
		return p, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return p, err
	}
	p.ID = id
	decoratePlaygroundExpiration(&p)
	return p, nil
}

// playgroundForInsert applies create-time defaults before persistence.
func playgroundForInsert(p domain.Playground, now time.Time) domain.Playground {
	p.Name = strings.TrimSpace(p.Name)
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Status == "" {
		p.Status = domain.StatusPending
	}
	if p.ServiceBranches == nil {
		p.ServiceBranches = map[string]any{}
	}
	if p.EnvOverrides == nil {
		p.EnvOverrides = map[string]string{}
	}
	if p.ComposeProject == nil {
		project := slug(p.Name) + "-" + strconv.FormatInt(now.Unix(), 36)
		p.ComposeProject = &project
	}
	return p
}

// GetPlayground fetches a Playground by ID or name.
func (s *DB) GetPlayground(ctx context.Context, identifier string) (domain.Playground, error) {
	where, arg := identifierWhereWithAlias(identifier, "pg")
	return queryOne(ctx, s.db, playgroundSelectSQL()+` WHERE `+where, arg, scanPlayground)
}

// SavePlayground updates a Playground and derived expiration metadata.
func (s *DB) SavePlayground(ctx context.Context, p domain.Playground) (domain.Playground, error) {
	now := time.Now().UTC()
	current, err := s.GetPlayground(ctx, strconv.FormatInt(p.ID, 10))
	if err == nil {
		if current.UpdatedAt.After(p.UpdatedAt) {
			p.Name = current.Name
		}
	} else if !errors.Is(err, ErrNotFound) {
		return p, err
	}
	p.Name = strings.TrimSpace(p.Name)
	encoded, err := encodePlaygroundJSON(p)
	if err != nil {
		return p, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE playgrounds SET name=?,status=?,playspec_id=?,marquee_id=?,compose_project=?,root_domain=?,routing_scheme=?,internal_password=?,env_overrides_json=?,service_branches_json=?,generated_compose_yaml=?,services_json=?,service_urls_json=?,build_statuses_json=?,creation_steps_json=?,expires_at=?,last_applied_at=?,error_message=?,state_reason=?,state_reasons_json=?,build_warnings_json=?,error_details_json=?,playguard_repair_reason=?,playguard_repair_lock_until=?,needs_recreation=?,updated_at=? WHERE id=?`,
		p.Name, p.Status, nullableInt64(p.PlayspecID), nullableInt64(p.MarqueeID), nullableString(p.ComposeProject), nullableString(p.RootDomain), nullableString(p.RoutingScheme), nullableString(p.InternalPassword), encoded.Env, encoded.Branches, p.GeneratedComposeYAML, encoded.Services, encoded.URLs, encoded.Builds, encoded.Steps, nullableTime(p.ExpiresAt), nullableTime(p.LastAppliedAt), nullableString(p.ErrorMessage), nullableString(p.StateReason), encoded.StateReasons, encoded.BuildWarnings, encoded.ErrorDetails, nullableString(p.PlayguardRepairReason), nullableTime(p.PlayguardRepairLockUntil), boolValue(p.NeedsRecreation), encodeTime(now), p.ID)
	if err := requireRowsAffected(res, err); err != nil {
		return p, err
	}
	p.UpdatedAt = now
	decoratePlaygroundExpiration(&p)
	return p, nil
}

// SavePlaygroundIfCurrent updates a Playground only if status and updated_at still match.
func (s *DB) SavePlaygroundIfCurrent(ctx context.Context, p domain.Playground, status string, updatedAt time.Time) (domain.Playground, bool, error) {
	now := time.Now().UTC()
	p.Name = strings.TrimSpace(p.Name)
	encoded, err := encodePlaygroundJSON(p)
	if err != nil {
		return p, false, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE playgrounds SET name=?,status=?,playspec_id=?,marquee_id=?,compose_project=?,root_domain=?,routing_scheme=?,internal_password=?,env_overrides_json=?,service_branches_json=?,generated_compose_yaml=?,services_json=?,service_urls_json=?,build_statuses_json=?,creation_steps_json=?,expires_at=?,last_applied_at=?,error_message=?,state_reason=?,state_reasons_json=?,build_warnings_json=?,error_details_json=?,playguard_repair_reason=?,playguard_repair_lock_until=?,needs_recreation=?,updated_at=? WHERE id=? AND status=? AND updated_at=?`,
		p.Name, p.Status, nullableInt64(p.PlayspecID), nullableInt64(p.MarqueeID), nullableString(p.ComposeProject), nullableString(p.RootDomain), nullableString(p.RoutingScheme), nullableString(p.InternalPassword), encoded.Env, encoded.Branches, p.GeneratedComposeYAML, encoded.Services, encoded.URLs, encoded.Builds, encoded.Steps, nullableTime(p.ExpiresAt), nullableTime(p.LastAppliedAt), nullableString(p.ErrorMessage), nullableString(p.StateReason), encoded.StateReasons, encoded.BuildWarnings, encoded.ErrorDetails, nullableString(p.PlayguardRepairReason), nullableTime(p.PlayguardRepairLockUntil), boolValue(p.NeedsRecreation), encodeTime(now), p.ID, status, encodeTime(updatedAt))
	if err != nil {
		return p, false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return p, false, err
	}
	if rows == 0 {
		current, getErr := s.GetPlayground(ctx, strconv.FormatInt(p.ID, 10))
		if getErr != nil {
			return p, false, getErr
		}
		return current, false, nil
	}
	p.UpdatedAt = now
	decoratePlaygroundExpiration(&p)
	return p, true, nil
}

// RenamePlayground updates a Playground name without rewriting runtime progress fields.
func (s *DB) RenamePlayground(ctx context.Context, id int64, name string) (domain.Playground, error) {
	now := time.Now().UTC()
	normalized := strings.TrimSpace(name)
	res, err := s.db.ExecContext(ctx, `UPDATE playgrounds SET name=?, updated_at=? WHERE id=?`, normalized, encodeTime(now), id)
	if err := requireRowsAffected(res, err); err != nil {
		return domain.Playground{}, err
	}
	return s.GetPlayground(ctx, strconv.FormatInt(id, 10))
}

// DeletePlayground deletes a Playground by ID or name.
func (s *DB) DeletePlayground(ctx context.Context, identifier string) error {
	return s.deleteByIdentifier(ctx, identifier, `DELETE FROM playgrounds WHERE id=?`, `DELETE FROM playgrounds WHERE name=?`)
}

// count executes a COUNT query and returns the integer result.
func (s *DB) count(ctx context.Context, query string, args ...any) (int, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

package storage

import (
	"context"
	"strings"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// ListPlayspecs returns all Playspecs with lock metadata.
func (s *DB) ListPlayspecs(ctx context.Context) ([]domain.Playspec, error) {
	result, err := queryRows(ctx, s.db, `SELECT id,name,description,base_compose_yaml,services_json,created_at,updated_at FROM playspecs ORDER BY id`, scanPlayspec)
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i], err = s.decoratePlayspecLock(ctx, result[i])
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// CreatePlayspec inserts a stateless Playspec.
func (s *DB) CreatePlayspec(ctx context.Context, p domain.Playspec) (domain.Playspec, error) {
	now := time.Now().UTC()
	p.Name = strings.TrimSpace(p.Name)
	p.PersistVolumes = new(false)
	services, err := encodeStoredJSON("playspecs.services_json", p.Services, "[]")
	if err != nil {
		return p, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO playspecs (name,description,base_compose_yaml,services_json,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
		p.Name, nullableString(p.Description), p.BaseComposeYAML, services, encodeTime(now), encodeTime(now))
	if err != nil {
		return p, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return p, err
	}
	p.ID = &id
	p.CreatedAt = &now
	p.UpdatedAt = &now
	p.Locked = new(false)
	count := int64(0)
	p.PlaygroundCount = &count
	return p, nil
}

// GetPlayspec fetches a Playspec by ID or name.
func (s *DB) GetPlayspec(ctx context.Context, identifier string) (domain.Playspec, error) {
	where, arg := identifierWhere(identifier)
	p, err := queryOne(ctx, s.db, `SELECT id,name,description,base_compose_yaml,services_json,created_at,updated_at FROM playspecs WHERE `+where, arg, scanPlayspec)
	if err != nil {
		return p, err
	}
	return s.decoratePlayspecLock(ctx, p)
}

// SavePlayspec updates an existing Playspec row.
func (s *DB) SavePlayspec(ctx context.Context, p domain.Playspec) (domain.Playspec, error) {
	if p.ID == nil {
		return p, ErrNotFound
	}
	now := time.Now().UTC()
	p.Name = strings.TrimSpace(p.Name)
	services, err := encodeStoredJSON("playspecs.services_json", p.Services, "[]")
	if err != nil {
		return p, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE playspecs SET name=?,description=?,base_compose_yaml=?,services_json=?,updated_at=? WHERE id=?`,
		p.Name, nullableString(p.Description), p.BaseComposeYAML, services, encodeTime(now), *p.ID)
	if err := requireRowsAffected(res, err); err != nil {
		return p, err
	}
	p.UpdatedAt = &now
	p.PersistVolumes = new(false)
	return s.decoratePlayspecLock(ctx, p)
}

// DeletePlayspec deletes a Playspec by ID or name.
func (s *DB) DeletePlayspec(ctx context.Context, identifier string) error {
	return s.deleteByIdentifier(ctx, identifier, `DELETE FROM playspecs WHERE id=?`, `DELETE FROM playspecs WHERE name=?`)
}

// CountPlaygroundsForPlayspec counts Playgrounds using a Playspec.
func (s *DB) CountPlaygroundsForPlayspec(ctx context.Context, playspecID int64) (int, error) {
	return s.count(ctx, `SELECT COUNT(*) FROM playgrounds WHERE playspec_id=?`, playspecID)
}

// decoratePlayspecLock derives lock metadata from current Playground references.
func (s *DB) decoratePlayspecLock(ctx context.Context, p domain.Playspec) (domain.Playspec, error) {
	if p.ID == nil {
		return p, nil
	}
	count, err := s.CountPlaygroundsForPlayspec(ctx, *p.ID)
	if err != nil {
		return p, err
	}
	value := int64(count)
	locked := count > 0
	p.PlaygroundCount = &value
	p.Locked = &locked
	return p, nil
}

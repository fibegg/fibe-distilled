package storage

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

// migrationFiles embeds SQLite schema migrations for the single binary.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// sqlMigration is one embedded schema migration.
type sqlMigration struct {
	version int64
	name    string
	upSQL   string
}

// migrate applies embedded SQL migrations.
func (s *DB) migrate(ctx context.Context) error {
	state, err := s.migrationState(ctx)
	if err != nil {
		return err
	}
	return s.applyPendingMigrations(ctx, state.migrations, state.applied)
}

// migrationPlan carries available migrations and applied version IDs.
type migrationPlan struct {
	migrations []sqlMigration
	applied    map[int64]bool
}

// migrationState loads migrations and already-applied versions.
func (s *DB) migrationState(ctx context.Context) (migrationPlan, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return migrationPlan{}, err
	}
	if err := s.ensureMigrationVersionTable(ctx); err != nil {
		return migrationPlan{}, err
	}
	applied, err := s.appliedMigrationVersions(ctx)
	if err != nil {
		return migrationPlan{}, err
	}
	return migrationPlan{migrations: migrations, applied: applied}, nil
}

// applyPendingMigrations applies migrations not present in the version table.
func (s *DB) applyPendingMigrations(ctx context.Context, migrations []sqlMigration, applied map[int64]bool) error {
	for _, migration := range migrations {
		if applied[migration.version] {
			continue
		}
		if err := s.applyMigration(ctx, migration); err != nil {
			return err
		}
	}
	return nil
}

// ensureMigrationVersionTable creates the Goose-compatible version table.
func (s *DB) ensureMigrationVersionTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS goose_db_version (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  version_id INTEGER NOT NULL,
  is_applied INTEGER NOT NULL,
  tstamp TIMESTAMP DEFAULT (datetime('now'))
)`)
	return err
}

// appliedMigrationVersions returns applied schema versions by number.
func (s *DB) appliedMigrationVersions(ctx context.Context) (versions map[int64]bool, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version_id FROM goose_db_version WHERE is_applied=1`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	versions = map[int64]bool{}
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions[version] = true
	}
	return versions, rows.Err()
}

// applyMigration runs one migration and records its version atomically.
func (s *DB) applyMigration(ctx context.Context, migration sqlMigration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := execMigrationTx(ctx, tx, migration); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("apply migration %s: %w", migration.name, err),
				fmt.Errorf("rollback migration %s: %w", migration.name, rollbackErr),
			)
		}
		return fmt.Errorf("apply migration %s: %w", migration.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", migration.name, err)
	}
	return nil
}

// execMigrationTx applies migration SQL and inserts the version row.
func execMigrationTx(ctx context.Context, tx *sql.Tx, migration sqlMigration) error {
	if strings.TrimSpace(migration.upSQL) != "" {
		if _, err := tx.ExecContext(ctx, migration.upSQL); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, migration.version)
	return err
}

// loadMigrations reads all embedded migrations in version order.
func loadMigrations() ([]sqlMigration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	migrations, err := readMigrationEntries(entries)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(migrations, func(left, right sqlMigration) int {
		return cmp.Or(cmp.Compare(left.version, right.version), cmp.Compare(left.name, right.name))
	})
	if err := rejectDuplicateMigrationVersions(migrations); err != nil {
		return nil, err
	}
	return migrations, nil
}

// readMigrationEntries reads SQL migration files from embedded directory entries.
func readMigrationEntries(entries []fs.DirEntry) ([]sqlMigration, error) {
	migrations := make([]sqlMigration, 0, len(entries))
	for _, entry := range entries {
		if !isSQLMigrationEntry(entry) {
			continue
		}
		migration, err := readMigration(entry.Name())
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, migration)
	}
	return migrations, nil
}

// isSQLMigrationEntry reports whether an embedded entry is a migration file.
func isSQLMigrationEntry(entry fs.DirEntry) bool {
	return !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql")
}

// rejectDuplicateMigrationVersions fails closed on ambiguous schema manifests.
func rejectDuplicateMigrationVersions(migrations []sqlMigration) error {
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version == migrations[i].version {
			return fmt.Errorf("duplicate migration version %d: %s and %s", migrations[i].version, migrations[i-1].name, migrations[i].name)
		}
	}
	return nil
}

// readMigration parses one embedded SQL migration file.
func readMigration(name string) (sqlMigration, error) {
	version, err := migrationVersion(name)
	if err != nil {
		return sqlMigration{}, err
	}
	content, err := migrationFiles.ReadFile(path.Join("migrations", name))
	if err != nil {
		return sqlMigration{}, err
	}
	upSQL, err := migrationUpSQL(string(content), name)
	if err != nil {
		return sqlMigration{}, err
	}
	return sqlMigration{version: version, name: name, upSQL: upSQL}, nil
}

// migrationVersion extracts the numeric filename prefix.
func migrationVersion(name string) (int64, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		prefix = strings.TrimSuffix(name, ".sql")
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid migration filename %s", name)
	}
	return version, nil
}

// migrationUpSQL extracts the forward SQL block from a migration file.
func migrationUpSQL(content string, name string) (string, error) {
	_, upSQL, found := strings.Cut(content, "-- +goose Up")
	if !found {
		return "", fmt.Errorf("migration %s missing up marker", name)
	}
	if up, _, found := strings.Cut(upSQL, "-- +goose Down"); found {
		upSQL = up
	}
	return strings.TrimSpace(upSQL), nil
}

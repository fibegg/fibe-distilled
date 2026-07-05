package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// ErrNotFound is returned when a named or ID-based lookup misses.
var ErrNotFound = errors.New("not found")

// ConfiguredMarqueeName is the persisted name for the startup-configured Marquee.
const ConfiguredMarqueeName = "default"

// DB provides SQLite-backed repositories for fibe-distilled domain objects.
type DB struct {
	db *sql.DB
}

// scanRows scans all rows and closes the cursor without hiding close errors.
func scanRows[T any](rows *sql.Rows, scan func(scanner) (T, error)) (items []T, err error) {
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		item, scanErr := scan(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// queryRows runs a list query and scans all returned rows.
func queryRows[T any](ctx context.Context, db *sql.DB, query string, scan func(scanner) (T, error), args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scan)
}

// queryOne scans one row and maps sql.ErrNoRows to ErrNotFound.
func queryOne[T any](ctx context.Context, db *sql.DB, query string, arg any, scan func(scanner) (T, error)) (T, error) {
	item, err := scan(db.QueryRowContext(ctx, query, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

// IsUniqueConstraint reports whether an error came from a SQLite uniqueness check.
func IsUniqueConstraint(err error) bool {
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code == sqlite3.ErrConstraint &&
		(sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique ||
			sqliteErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey)
}

// Open connects to SQLite, applies pragmas, runs migrations, and returns a DB.
func Open(ctx context.Context, path string) (opened *DB, err error) {
	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, db.Close())
		}
	}()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := applySQLitePragmas(ctx, db); err != nil {
		return nil, err
	}
	s := &DB{db: db}
	if err := s.migrate(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// applySQLitePragmas enables the local SQLite settings fibe-distilled relies on.
func applySQLitePragmas(ctx context.Context, db *sql.DB) error {
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA mmap_size = 0",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	return nil
}

// sqliteDSN applies SQLite pragmas to plain filesystem database paths.
func sqliteDSN(path string) string {
	if strings.HasPrefix(path, "file:") {
		return path
	}
	return "file:" + path + "?_foreign_keys=1&_busy_timeout=5000&_journal_mode=WAL&_mutex=full"
}

// Close closes the underlying SQLite handle.
func (s *DB) Close() error {
	return s.db.Close()
}

// ServerID returns the stable per-database fibe-distilled server ID.
func (s *DB) ServerID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM server_metadata WHERE key='server_id'`).Scan(&id)
	if err == nil && strings.TrimSpace(id) != "" {
		return id, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id, err = randomHex(16)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO server_metadata (key,value,updated_at) VALUES ('server_id',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		id, encodeTime(now))
	if err != nil {
		return "", err
	}
	return id, nil
}

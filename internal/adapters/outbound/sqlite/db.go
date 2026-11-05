// Package sqlite implements the persistence ports on SQLite (WAL) with FTS5.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hootyeah/hootdex/migrations"

	_ "modernc.org/sqlite" // pure-Go driver with FTS5 compiled in (works on linux/amd64 and darwin/arm64 without CGO)
)

// Required per-connection pragmas (SCHEMA.md §1). busy_timeout is applied first so the
// journal_mode switch itself waits on a locked database instead of failing.
var connectionPragmas = []string{
	"busy_timeout(5000)",
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
	"foreign_keys(ON)",
}

// DB wraps *sql.DB for the repositories.
type DB struct {
	*sql.DB
	path string
}

// Open opens (creating if needed) the database at path, applies the pragmas on every
// pooled connection, and runs pending migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("sqlite.Open [%s]: mkdir: %w", path, err)
		}
	}
	q := url.Values{}
	for _, p := range connectionPragmas {
		q.Add("_pragma", p)
	}
	// IMMEDIATE transactions take the write lock up-front, which avoids
	// SQLITE_BUSY lock-upgrade failures for read-then-write transactions under WAL.
	q.Set("_txlock", "immediate")
	dsn := "file:" + path + "?" + q.Encode()

	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite.Open [%s]: %w", path, err)
	}
	sqldb.SetMaxOpenConns(8)
	db := &DB{DB: sqldb, path: path}
	if err := db.PingContext(ctx); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("sqlite.Open [%s]: ping: %w", path, err)
	}
	if err := db.Migrate(ctx); err != nil {
		_ = sqldb.Close()
		return nil, err
	}
	return db, nil
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// Migrate applies every embedded migration that has not been recorded yet.
func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return fmt.Errorf("sqlite.Migrate: create schema_migrations: %w", err)
	}
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return fmt.Errorf("sqlite.Migrate: list: %w", err)
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n); err != nil {
			return fmt.Errorf("sqlite.Migrate [%s]: check: %w", version, err)
		}
		if n > 0 {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("sqlite.Migrate [%s]: read: %w", version, err)
		}
		if err := db.inTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("exec: %w", err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, version)
			return err
		}); err != nil {
			return fmt.Errorf("sqlite.Migrate [%s]: %w", version, err)
		}
	}
	return nil
}

// inTx runs fn in a transaction, rolling back on error.
func (db *DB) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("%w (rollback: %v)", err, rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// --- nullable helpers -------------------------------------------------------

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func nullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func nullFloat(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func int64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

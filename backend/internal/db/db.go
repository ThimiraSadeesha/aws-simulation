// Package db opens the SQLite database and applies migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"cloudsim/backend/migrations"
)

// TimeFormat is the canonical timestamp format stored in TEXT columns (sortable).
const TimeFormat = "2006-01-02T15:04:05.000Z"

// Now returns the current UTC time formatted for storage.
func Now() string { return time.Now().UTC().Format(TimeFormat) }

// Fmt formats t for storage.
func Fmt(t time.Time) string { return t.UTC().Format(TimeFormat) }

// Parse parses a stored timestamp (zero time on error).
func Parse(s string) time.Time {
	t, err := time.Parse(TimeFormat, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339Nano, s)
	}
	return t
}

// DB wraps *sql.DB with the source path (needed for backup/restore).
type DB struct {
	*sql.DB
	Path string
}

// Open opens (creating if needed) the database with WAL, foreign keys and a busy timeout.
func Open(path string) (*DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sqldb.SetMaxOpenConns(8)
	sqldb.SetMaxIdleConns(8)
	sqldb.SetConnMaxIdleTime(5 * time.Minute)
	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, err
	}
	return &DB{DB: sqldb, Path: path}, nil
}

// Migrate applies all pending migrations, each in its own transaction.
func (d *DB) Migrate(ctx context.Context) ([]string, error) {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	entries, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	var applied []string
	for _, name := range entries {
		version := strings.TrimSuffix(name, ".sql")
		var exists int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&exists); err != nil {
			return applied, err
		}
		if exists > 0 {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return applied, err
		}
		err = d.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, Now())
			return err
		})
		if err != nil {
			return applied, err
		}
		applied = append(applied, version)
	}
	return applied, nil
}

// Tx runs fn in a transaction, committing on nil error and rolling back otherwise.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// IntegrityReport runs integrity and foreign key checks.
type IntegrityReport struct {
	IntegrityCheck       string   `json:"integrity_check"`
	ForeignKeyViolations []string `json:"foreign_key_violations"`
}

func (d *DB) Integrity(ctx context.Context) (*IntegrityReport, error) {
	r := &IntegrityReport{ForeignKeyViolations: []string{}}
	if err := d.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&r.IntegrityCheck); err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return nil, err
		}
		r.ForeignKeyViolations = append(r.ForeignKeyViolations, fmt.Sprintf("%s rowid=%d -> %s", table, rowid.Int64, parent))
	}
	return r, rows.Err()
}

// Backup writes a consistent snapshot to dst using VACUUM INTO.
func (d *DB) Backup(ctx context.Context, dst string) error {
	if strings.ContainsAny(dst, "'\x00") {
		return fmt.Errorf("invalid backup path")
	}
	_, err := d.ExecContext(ctx, `VACUUM INTO '`+dst+`'`)
	return err
}

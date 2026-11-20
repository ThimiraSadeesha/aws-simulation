// Package repositories contains the SQLite data-access layer. Business logic in
// internal/services depends on the interfaces declared here, never on SQL.
package repositories

import (
	"context"
	"database/sql"
	"errors"

	"cloudsim/backend/internal/db"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Querier is satisfied by *sql.DB and *sql.Tx so repos work inside transactions.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Store bundles all repositories over one database.
type Store struct {
	DB       *db.DB
	Users    UserRepo
	Sessions SessionRepo
	IAM      IAMRepo
	Events   EventRepo
	Audit    AuditRepo
	Tags     TagRepo
	Network  NetworkRepo
	EC2      EC2Repo
	KV       KVRepo
}

func NewStore(d *db.DB) *Store {
	return &Store{
		DB:       d,
		Users:    &userRepo{d},
		Sessions: &sessionRepo{d},
		IAM:      &iamRepo{d},
		Events:   &eventRepo{d},
		Audit:    &auditRepo{d},
		Tags:     &tagRepo{d},
		Network:  &networkRepo{d},
		EC2:      &ec2Repo{d},
		KV:       &kvRepo{d},
	}
}

// KVRepo stores small pieces of process state (e.g. last engine event seq).
type KVRepo interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

type kvRepo struct{ d *db.DB }

func (r *kvRepo) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := r.d.QueryRowContext(ctx, `SELECT value FROM kv_state WHERE key=?`, key).Scan(&v)
	return v, notFound(err)
}

func (r *kvRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.d.ExecContext(ctx, `INSERT INTO kv_state(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

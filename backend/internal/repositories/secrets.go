package repositories

import (
	"context"
	"database/sql"

	"cloudsim/backend/internal/db"
)

// Secret is secret metadata (the value lives encrypted in secret_versions).
type Secret struct {
	SecretID         string `json:"secret_id"`
	AccountID        string `json:"-"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	CurrentVersionID string `json:"current_version_id"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	LastAccessedAt   string `json:"last_accessed_at,omitempty"`
}

// SecretVersion holds one encrypted value.
type SecretVersion struct {
	VersionID  string `json:"version_id"`
	SecretID   string `json:"-"`
	Ciphertext []byte `json:"-"`
	Stage      string `json:"-"`
	CreatedAt  string `json:"created_at"`
}

type SecretsRepo interface {
	// Create inserts the secret and its first version (AWSCURRENT) atomically.
	Create(ctx context.Context, s *Secret, v *SecretVersion) error
	Get(ctx context.Context, account, name string) (*Secret, error)
	List(ctx context.Context, account string) ([]Secret, error)
	UpdateDescription(ctx context.Context, id, desc, now string) error
	// PutVersion adds a new AWSCURRENT version, demoting the previous one to AWSPREVIOUS.
	PutVersion(ctx context.Context, secretID string, v *SecretVersion) error
	Versions(ctx context.Context, secretID string) ([]SecretVersion, error)
	GetVersion(ctx context.Context, secretID, versionID string) (*SecretVersion, error)
	Touch(ctx context.Context, id, now string) error
	Delete(ctx context.Context, id string) error
}

type secretsRepo struct{ d *db.DB }

func NewSecretsRepo(d *db.DB) SecretsRepo { return &secretsRepo{d} }

const secretCols = `secret_id, account_id, name, description, current_version_id, created_at, updated_at, COALESCE(last_accessed_at,'')`

func scanSecret(sc interface{ Scan(...any) error }) (*Secret, error) {
	var s Secret
	err := sc.Scan(&s.SecretID, &s.AccountID, &s.Name, &s.Description, &s.CurrentVersionID, &s.CreatedAt, &s.UpdatedAt, &s.LastAccessedAt)
	return &s, err
}

func (r *secretsRepo) Create(ctx context.Context, s *Secret, v *SecretVersion) error {
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO secrets(secret_id, account_id, name, description, current_version_id, created_at, updated_at)
 VALUES(?,?,?,?,?,?,?)`, s.SecretID, s.AccountID, s.Name, s.Description, v.VersionID, s.CreatedAt, s.UpdatedAt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO secret_versions(version_id, secret_id, ciphertext, stage, created_at) VALUES(?,?,?,?,?)`,
			v.VersionID, s.SecretID, v.Ciphertext, "AWSCURRENT", v.CreatedAt)
		return err
	})
}

func (r *secretsRepo) Get(ctx context.Context, account, name string) (*Secret, error) {
	s, err := scanSecret(r.d.QueryRowContext(ctx, `SELECT `+secretCols+` FROM secrets WHERE account_id=? AND name=?`, account, name))
	return s, notFound(err)
}

func (r *secretsRepo) List(ctx context.Context, account string) ([]Secret, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+secretCols+` FROM secrets WHERE account_id=? ORDER BY name`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Secret{}
	for rows.Next() {
		s, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *secretsRepo) UpdateDescription(ctx context.Context, id, desc, now string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE secrets SET description=?, updated_at=? WHERE secret_id=?`, desc, now, id)
	return err
}

func (r *secretsRepo) PutVersion(ctx context.Context, secretID string, v *SecretVersion) error {
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE secret_versions SET stage='' WHERE secret_id=? AND stage='AWSPREVIOUS'`, secretID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE secret_versions SET stage='AWSPREVIOUS' WHERE secret_id=? AND stage='AWSCURRENT'`, secretID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO secret_versions(version_id, secret_id, ciphertext, stage, created_at) VALUES(?,?,?,?,?)`,
			v.VersionID, secretID, v.Ciphertext, "AWSCURRENT", v.CreatedAt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE secrets SET current_version_id=?, updated_at=? WHERE secret_id=?`, v.VersionID, v.CreatedAt, secretID)
		return err
	})
}

func (r *secretsRepo) Versions(ctx context.Context, secretID string) ([]SecretVersion, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT version_id, secret_id, stage, created_at FROM secret_versions WHERE secret_id=? ORDER BY created_at DESC, rowid DESC`, secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecretVersion{}
	for rows.Next() {
		var v SecretVersion
		if err := rows.Scan(&v.VersionID, &v.SecretID, &v.Stage, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *secretsRepo) GetVersion(ctx context.Context, secretID, versionID string) (*SecretVersion, error) {
	var v SecretVersion
	err := r.d.QueryRowContext(ctx, `SELECT version_id, secret_id, ciphertext, stage, created_at FROM secret_versions WHERE secret_id=? AND version_id=?`,
		secretID, versionID).Scan(&v.VersionID, &v.SecretID, &v.Ciphertext, &v.Stage, &v.CreatedAt)
	return &v, notFound(err)
}

func (r *secretsRepo) Touch(ctx context.Context, id, now string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE secrets SET last_accessed_at=? WHERE secret_id=?`, now, id)
	return err
}

func (r *secretsRepo) Delete(ctx context.Context, id string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM secrets WHERE secret_id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

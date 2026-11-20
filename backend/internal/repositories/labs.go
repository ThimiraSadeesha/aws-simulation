package repositories

import (
	"context"
	"database/sql"

	"cloudsim/backend/internal/db"
)

// LabProgress is an account's progress in one lab.
type LabProgress struct {
	ID          int64
	AccountID   string
	LabID       string
	Status      string
	TotalSteps  int
	StartedAt   string
	CompletedAt *string
	UpdatedAt   string
	Steps       []LabStepResult
}

// LabStepResult is the latest validation result of a step.
type LabStepResult struct {
	Index     int    `json:"index"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Details   string `json:"-"`
	Attempts  int    `json:"attempts"`
	CheckedAt string `json:"checked_at"`
}

// LabRepo persists lab progress.
type LabRepo interface {
	Get(ctx context.Context, account, labID string) (*LabProgress, error)
	List(ctx context.Context, account string) ([]LabProgress, error)
	Start(ctx context.Context, account, labID string, totalSteps int) (*LabProgress, error)
	RecordStep(ctx context.Context, account, labID string, totalSteps int, r LabStepResult) (*LabProgress, error)
	Delete(ctx context.Context, account, labID string) error
}

type labRepo struct{ d *db.DB }

// NewLabRepo returns the SQLite lab progress repository.
func NewLabRepo(d *db.DB) LabRepo { return &labRepo{d} }

func (r *labRepo) get(ctx context.Context, q Querier, account, labID string) (*LabProgress, error) {
	p := &LabProgress{}
	var completed sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id, account_id, lab_id, status, total_steps, started_at, completed_at, updated_at
		FROM lab_progress WHERE account_id=? AND lab_id=?`, account, labID).
		Scan(&p.ID, &p.AccountID, &p.LabID, &p.Status, &p.TotalSteps, &p.StartedAt, &completed, &p.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if completed.Valid {
		p.CompletedAt = &completed.String
	}
	rows, err := q.QueryContext(ctx, `SELECT step_index, status, message, details, attempts, checked_at FROM lab_step_results
		WHERE progress_id=? ORDER BY step_index`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s LabStepResult
		if err := rows.Scan(&s.Index, &s.Status, &s.Message, &s.Details, &s.Attempts, &s.CheckedAt); err != nil {
			return nil, err
		}
		p.Steps = append(p.Steps, s)
	}
	return p, rows.Err()
}

func (r *labRepo) Get(ctx context.Context, account, labID string) (*LabProgress, error) {
	return r.get(ctx, r.d, account, labID)
}

func (r *labRepo) List(ctx context.Context, account string) ([]LabProgress, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT lab_id FROM lab_progress WHERE account_id=? ORDER BY updated_at DESC`, account)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]LabProgress, 0, len(ids))
	for _, id := range ids {
		p, err := r.get(ctx, r.d, account, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

func (r *labRepo) start(ctx context.Context, q Querier, account, labID string, total int) error {
	now := db.Now()
	_, err := q.ExecContext(ctx, `INSERT INTO lab_progress(account_id, lab_id, status, total_steps, started_at, updated_at)
		VALUES(?,?,?,?,?,?) ON CONFLICT(account_id, lab_id) DO UPDATE SET total_steps=excluded.total_steps, updated_at=excluded.updated_at`,
		account, labID, "in_progress", total, now, now)
	return err
}

func (r *labRepo) Start(ctx context.Context, account, labID string, total int) (*LabProgress, error) {
	if err := r.start(ctx, r.d, account, labID, total); err != nil {
		return nil, err
	}
	return r.Get(ctx, account, labID)
}

// RecordStep stores a step result and recomputes completion atomically.
func (r *labRepo) RecordStep(ctx context.Context, account, labID string, total int, s LabStepResult) (*LabProgress, error) {
	tx, err := r.d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := r.start(ctx, tx, account, labID, total); err != nil {
		return nil, err
	}
	var pid int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM lab_progress WHERE account_id=? AND lab_id=?`, account, labID).Scan(&pid); err != nil {
		return nil, err
	}
	now := db.Now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO lab_step_results(progress_id, step_index, status, message, details, attempts, checked_at)
		VALUES(?,?,?,?,?,1,?) ON CONFLICT(progress_id, step_index) DO UPDATE SET status=excluded.status, message=excluded.message,
		details=excluded.details, attempts=lab_step_results.attempts+1, checked_at=excluded.checked_at`,
		pid, s.Index, s.Status, s.Message, s.Details, now); err != nil {
		return nil, err
	}
	var passed int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM lab_step_results WHERE progress_id=? AND status='passed' AND step_index < ?`, pid, total).Scan(&passed); err != nil {
		return nil, err
	}
	if passed >= total {
		_, err = tx.ExecContext(ctx, `UPDATE lab_progress SET status='completed', completed_at=COALESCE(completed_at, ?), updated_at=? WHERE id=?`, now, now, pid)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE lab_progress SET status='in_progress', completed_at=NULL, updated_at=? WHERE id=?`, now, pid)
	}
	if err != nil {
		return nil, err
	}
	p, err := r.get(ctx, tx, account, labID)
	if err != nil {
		return nil, err
	}
	return p, tx.Commit()
}

func (r *labRepo) Delete(ctx context.Context, account, labID string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM lab_progress WHERE account_id=? AND lab_id=?`, account, labID)
	return err
}

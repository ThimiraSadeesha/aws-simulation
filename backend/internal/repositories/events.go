package repositories

import (
	"context"
	"encoding/json"
	"strings"

	"cloudsim/backend/internal/db"
)

type Event struct {
	ID           int64           `json:"id"`
	AccountID    string          `json:"-"`
	TS           string          `json:"ts"`
	ResourceID   string          `json:"resource_id"`
	ResourceType string          `json:"resource_type"`
	Type         string          `json:"type"`
	Message      string          `json:"message"`
	Detail       json.RawMessage `json:"detail"`
}

type EventFilter struct {
	AccountID  string
	ResourceID string
	Type       string
	Limit      int
	Offset     int
}

type EventRepo interface {
	Insert(ctx context.Context, e *Event) error
	List(ctx context.Context, f EventFilter) ([]Event, int, error)
	Prune(ctx context.Context, keepPerAccount int) error
}

type eventRepo struct{ d *db.DB }

func (r *eventRepo) Insert(ctx context.Context, e *Event) error {
	if e.TS == "" {
		e.TS = db.Now()
	}
	if len(e.Detail) == 0 {
		e.Detail = json.RawMessage("{}")
	}
	res, err := r.d.ExecContext(ctx, `INSERT INTO events(account_id,ts,resource_id,resource_type,type,message,detail) VALUES(?,?,?,?,?,?,?)`,
		e.AccountID, e.TS, e.ResourceID, e.ResourceType, e.Type, e.Message, string(e.Detail))
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

func (r *eventRepo) List(ctx context.Context, f EventFilter) ([]Event, int, error) {
	where := []string{"account_id = ?"}
	args := []any{f.AccountID}
	if f.ResourceID != "" {
		where = append(where, "resource_id = ?")
		args = append(args, f.ResourceID)
	}
	if f.Type != "" {
		where = append(where, "type = ?")
		args = append(args, f.Type)
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := r.d.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := r.d.QueryContext(ctx, `SELECT id,account_id,ts,resource_id,resource_type,type,message,detail FROM events WHERE `+w+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var detail string
		if err := rows.Scan(&e.ID, &e.AccountID, &e.TS, &e.ResourceID, &e.ResourceType, &e.Type, &e.Message, &detail); err != nil {
			return nil, 0, err
		}
		e.Detail = json.RawMessage(detail)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func (r *eventRepo) Prune(ctx context.Context, keep int) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM events WHERE id IN (
		SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY account_id ORDER BY id DESC) rn FROM events) WHERE rn > ?)`, keep)
	return err
}

type AuditEntry struct {
	ID         int64  `json:"id"`
	AccountID  string `json:"-"`
	TS         string `json:"ts"`
	Principal  string `json:"principal"`
	Action     string `json:"action"`
	Resource   string `json:"resource"`
	Result     string `json:"result"`
	StatusCode int    `json:"status_code"`
	RequestID  string `json:"request_id"`
	SourceIP   string `json:"source_ip"`
	Detail     string `json:"detail,omitempty"`
}

type AuditFilter struct {
	AccountID string
	Action    string
	Principal string
	Limit     int
	Offset    int
}

type AuditRepo interface {
	InsertBatch(ctx context.Context, entries []AuditEntry) error
	List(ctx context.Context, f AuditFilter) ([]AuditEntry, int, error)
}

type auditRepo struct{ d *db.DB }

func (r *auditRepo) InsertBatch(ctx context.Context, entries []AuditEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := r.d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO audit_logs(account_id,ts,principal,action,resource,result,status_code,request_id,source_ip,detail) VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, e.AccountID, e.TS, e.Principal, e.Action, e.Resource, e.Result, e.StatusCode, e.RequestID, e.SourceIP, e.Detail); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (r *auditRepo) List(ctx context.Context, f AuditFilter) ([]AuditEntry, int, error) {
	where := []string{"account_id = ?"}
	args := []any{f.AccountID}
	if f.Action != "" {
		where = append(where, "action LIKE ?")
		args = append(args, strings.ReplaceAll(f.Action, "*", "%"))
	}
	if f.Principal != "" {
		where = append(where, "principal = ?")
		args = append(args, f.Principal)
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := r.d.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := r.d.QueryContext(ctx, `SELECT id,account_id,ts,principal,action,resource,result,status_code,request_id,source_ip,detail FROM audit_logs WHERE `+w+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.AccountID, &e.TS, &e.Principal, &e.Action, &e.Resource, &e.Result, &e.StatusCode, &e.RequestID, &e.SourceIP, &e.Detail); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// TagRepo stores tags for any resource id.
type TagRepo interface {
	Get(ctx context.Context, resourceID string) (map[string]string, error)
	GetMany(ctx context.Context, ids []string) (map[string]map[string]string, error)
	Replace(ctx context.Context, q Querier, account, resourceID string, tags map[string]string) error
	FindByTag(ctx context.Context, account, key, value string) ([]string, error)
	Delete(ctx context.Context, q Querier, resourceID string) error
}

type tagRepo struct{ d *db.DB }

func (r *tagRepo) Get(ctx context.Context, id string) (map[string]string, error) {
	m, err := r.GetMany(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	if m[id] == nil {
		return map[string]string{}, nil
	}
	return m[id], nil
}

func (r *tagRepo) GetMany(ctx context.Context, ids []string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	// Chunk to stay under SQLite's variable limit.
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		chunk := ids[start:end]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := r.d.QueryContext(ctx, `SELECT resource_id,key,value FROM resource_tags WHERE resource_id IN (?`+strings.Repeat(",?", len(chunk)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, k, v string
			if err := rows.Scan(&id, &k, &v); err != nil {
				rows.Close()
				return nil, err
			}
			if out[id] == nil {
				out[id] = map[string]string{}
			}
			out[id][k] = v
		}
		rows.Close()
	}
	return out, nil
}

func (r *tagRepo) Replace(ctx context.Context, q Querier, account, id string, tags map[string]string) error {
	if q == nil {
		q = r.d
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM resource_tags WHERE resource_id=?`, id); err != nil {
		return err
	}
	for k, v := range tags {
		if _, err := q.ExecContext(ctx, `INSERT INTO resource_tags(resource_id,account_id,key,value) VALUES(?,?,?,?)`, id, account, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (r *tagRepo) FindByTag(ctx context.Context, account, key, value string) ([]string, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT resource_id FROM resource_tags WHERE account_id=? AND key=? AND (?='' OR value=?)`, account, key, value, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *tagRepo) Delete(ctx context.Context, q Querier, id string) error {
	if q == nil {
		q = r.d
	}
	_, err := q.ExecContext(ctx, `DELETE FROM resource_tags WHERE resource_id=?`, id)
	return err
}

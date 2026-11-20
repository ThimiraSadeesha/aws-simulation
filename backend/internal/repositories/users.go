package repositories

import (
	"context"
	"time"

	"cloudsim/backend/internal/db"
)

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	AccountID    string `json:"account_id"`
	CreatedAt    string `json:"created_at"`
}

type UserRepo interface {
	Create(ctx context.Context, u *User) error
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	GetByAccount(ctx context.Context, accountID string) (*User, error)
	Count(ctx context.Context) (int, error)
	List(ctx context.Context) ([]User, error)
	SetRole(ctx context.Context, id int64, role string) error
}

type userRepo struct{ d *db.DB }

const userCols = `id, username, email, password_hash, role, account_id, created_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.AccountID, &u.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

func (r *userRepo) Create(ctx context.Context, u *User) error {
	now := db.Now()
	res, err := r.d.ExecContext(ctx, `INSERT INTO users(username,email,password_hash,role,account_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		u.Username, u.Email, u.PasswordHash, u.Role, u.AccountID, now, now)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	u.CreatedAt = now
	_, err = r.d.ExecContext(ctx, `INSERT INTO projects(account_id,name,description,created_at) VALUES(?,?,?,?)`,
		u.AccountID, "default", "Default project", now)
	return err
}

func (r *userRepo) GetByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(r.d.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, username))
}

func (r *userRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(r.d.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (r *userRepo) GetByAccount(ctx context.Context, accountID string) (*User, error) {
	return scanUser(r.d.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE account_id = ?`, accountID))
}

func (r *userRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.d.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (r *userRepo) List(ctx context.Context) ([]User, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (r *userRepo) SetRole(ctx context.Context, id int64, role string) error {
	res, err := r.d.ExecContext(ctx, `UPDATE users SET role=?, updated_at=? WHERE id=?`, role, db.Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type Session struct {
	IDHash    string
	UserID    int64
	CSRFToken string
	ExpiresAt time.Time
}

type SessionRepo interface {
	Create(ctx context.Context, s *Session, ip, ua string) error
	Get(ctx context.Context, idHash string) (*Session, error)
	Touch(ctx context.Context, idHash string, expires time.Time) error
	Delete(ctx context.Context, idHash string) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type sessionRepo struct{ d *db.DB }

func (r *sessionRepo) Create(ctx context.Context, s *Session, ip, ua string) error {
	now := db.Now()
	_, err := r.d.ExecContext(ctx, `INSERT INTO sessions(id_hash,user_id,csrf_token,created_at,expires_at,last_seen,ip,user_agent) VALUES(?,?,?,?,?,?,?,?)`,
		s.IDHash, s.UserID, s.CSRFToken, now, db.Fmt(s.ExpiresAt), now, ip, ua)
	return err
}

func (r *sessionRepo) Get(ctx context.Context, idHash string) (*Session, error) {
	var s Session
	var exp string
	err := r.d.QueryRowContext(ctx, `SELECT id_hash,user_id,csrf_token,expires_at FROM sessions WHERE id_hash=?`, idHash).
		Scan(&s.IDHash, &s.UserID, &s.CSRFToken, &exp)
	if err != nil {
		return nil, notFound(err)
	}
	s.ExpiresAt = db.Parse(exp)
	return &s, nil
}

func (r *sessionRepo) Touch(ctx context.Context, idHash string, expires time.Time) error {
	_, err := r.d.ExecContext(ctx, `UPDATE sessions SET last_seen=?, expires_at=? WHERE id_hash=?`, db.Now(), db.Fmt(expires), idHash)
	return err
}

func (r *sessionRepo) Delete(ctx context.Context, idHash string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, idHash)
	return err
}

func (r *sessionRepo) DeleteExpired(ctx context.Context) (int64, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, db.Now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

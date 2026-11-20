package repositories

import (
	"context"
	"database/sql"
	"encoding/json"

	"cloudsim/backend/internal/db"
)

type IAMUser struct {
	ID        int64             `json:"-"`
	AccountID string            `json:"-"`
	UserName  string            `json:"user_name"`
	UserID    string            `json:"user_id"`
	Path      string            `json:"path"`
	ARN       string            `json:"arn"`
	Tags      map[string]string `json:"tags"`
	CreatedAt string            `json:"created_at"`
}

type IAMGroup struct {
	ID        int64  `json:"-"`
	AccountID string `json:"-"`
	GroupName string `json:"group_name"`
	GroupID   string `json:"group_id"`
	Path      string `json:"path"`
	ARN       string `json:"arn"`
	CreatedAt string `json:"created_at"`
}

type IAMRole struct {
	ID                 int64           `json:"-"`
	AccountID          string          `json:"-"`
	RoleName           string          `json:"role_name"`
	RoleID             string          `json:"role_id"`
	Path               string          `json:"path"`
	ARN                string          `json:"arn"`
	Description        string          `json:"description"`
	AssumeRolePolicy   json.RawMessage `json:"assume_role_policy_document"`
	MaxSessionDuration int             `json:"max_session_duration"`
	CreatedAt          string          `json:"created_at"`
}

type IAMPolicy struct {
	ID          int64           `json:"-"`
	AccountID   string          `json:"-"`
	PolicyName  string          `json:"policy_name"`
	PolicyID    string          `json:"policy_id"`
	ARN         string          `json:"arn"`
	Description string          `json:"description"`
	Document    json.RawMessage `json:"policy_document"`
	Version     int             `json:"default_version"`
	Builtin     bool            `json:"is_builtin"`
	Attachments int             `json:"attachment_count"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type InlinePolicy struct {
	PolicyName string          `json:"policy_name"`
	Document   json.RawMessage `json:"policy_document"`
}

type AccessKey struct {
	AccessKeyID      string  `json:"access_key_id"`
	AccountID        string  `json:"-"`
	IAMUserID        *int64  `json:"-"`
	RoleID           *int64  `json:"-"`
	SessionName      string  `json:"-"`
	SecretHash       string  `json:"-"`
	SessionTokenHash string  `json:"-"`
	Status           string  `json:"status"`
	ExpiresAt        *string `json:"expires_at,omitempty"`
	LastUsedAt       *string `json:"last_used_at"`
	CreatedAt        string  `json:"created_at"`
}

// PolicyRow is a (name, document) pair used for evaluation.
type PolicyRow struct {
	Name     string
	Document string
}

// Principal types for attachments.
const (
	PUser  = "user"
	PGroup = "group"
	PRole  = "role"
)

type IAMRepo interface {
	CreateUser(ctx context.Context, u *IAMUser) error
	GetUser(ctx context.Context, account, name string) (*IAMUser, error)
	GetUserByID(ctx context.Context, id int64) (*IAMUser, error)
	ListUsers(ctx context.Context, account string) ([]IAMUser, error)
	DeleteUser(ctx context.Context, id int64) error

	CreateGroup(ctx context.Context, g *IAMGroup) error
	GetGroup(ctx context.Context, account, name string) (*IAMGroup, error)
	ListGroups(ctx context.Context, account string) ([]IAMGroup, error)
	DeleteGroup(ctx context.Context, id int64) error
	AddMember(ctx context.Context, groupID, userID int64) error
	RemoveMember(ctx context.Context, groupID, userID int64) error
	GroupsForUser(ctx context.Context, userID int64) ([]IAMGroup, error)
	GroupMembers(ctx context.Context, groupID int64) ([]IAMUser, error)

	CreateRole(ctx context.Context, r *IAMRole) error
	GetRole(ctx context.Context, account, name string) (*IAMRole, error)
	GetRoleByID(ctx context.Context, id int64) (*IAMRole, error)
	ListRoles(ctx context.Context, account string) ([]IAMRole, error)
	DeleteRole(ctx context.Context, id int64) error

	UpsertBuiltinPolicy(ctx context.Context, p *IAMPolicy) error
	CreatePolicy(ctx context.Context, p *IAMPolicy) error
	GetPolicy(ctx context.Context, account, nameOrARN string) (*IAMPolicy, error)
	ListPolicies(ctx context.Context, account string) ([]IAMPolicy, error)
	UpdatePolicyDocument(ctx context.Context, id int64, doc string) error
	DeletePolicy(ctx context.Context, id int64) error
	Attach(ctx context.Context, policyID int64, account, ptype string, pid int64) error
	Detach(ctx context.Context, policyID int64, ptype string, pid int64) error
	AttachedPolicies(ctx context.Context, ptype string, pid int64) ([]IAMPolicy, error)
	PutInline(ctx context.Context, account, ptype string, pid int64, name, doc string) error
	DeleteInline(ctx context.Context, ptype string, pid int64, name string) error
	InlinePolicies(ctx context.Context, ptype string, pid int64) ([]InlinePolicy, error)
	DeletePrincipalPolicies(ctx context.Context, ptype string, pid int64) error

	// EffectivePolicies returns every identity policy that applies to a user (direct, inline, via groups) or role.
	EffectivePolicies(ctx context.Context, ptype string, pid int64) ([]PolicyRow, error)

	CreateAccessKey(ctx context.Context, k *AccessKey) error
	GetAccessKey(ctx context.Context, id string) (*AccessKey, error)
	ListAccessKeys(ctx context.Context, iamUserID int64) ([]AccessKey, error)
	SetAccessKeyStatus(ctx context.Context, id, status string) error
	DeleteAccessKey(ctx context.Context, id string) error
	TouchAccessKey(ctx context.Context, id string) error
	DeleteExpiredKeys(ctx context.Context) (int64, error)
}

type iamRepo struct{ d *db.DB }

func userARN(account, path, name string) string {
	return "arn:cloudsim:iam::" + account + ":user" + path + name
}

func (r *iamRepo) CreateUser(ctx context.Context, u *IAMUser) error {
	u.CreatedAt = db.Now()
	tags, _ := json.Marshal(u.Tags)
	res, err := r.d.ExecContext(ctx, `INSERT INTO iam_users(account_id,user_name,user_id,path,tags,created_at) VALUES(?,?,?,?,?,?)`,
		u.AccountID, u.UserName, u.UserID, u.Path, string(tags), u.CreatedAt)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	u.ARN = userARN(u.AccountID, u.Path, u.UserName)
	return nil
}

const iamUserCols = `id,account_id,user_name,user_id,path,tags,created_at`

func scanIAMUser(row interface{ Scan(...any) error }) (*IAMUser, error) {
	var u IAMUser
	var tags string
	if err := row.Scan(&u.ID, &u.AccountID, &u.UserName, &u.UserID, &u.Path, &tags, &u.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(tags), &u.Tags)
	if u.Tags == nil {
		u.Tags = map[string]string{}
	}
	u.ARN = userARN(u.AccountID, u.Path, u.UserName)
	return &u, nil
}

func (r *iamRepo) GetUser(ctx context.Context, account, name string) (*IAMUser, error) {
	return scanIAMUser(r.d.QueryRowContext(ctx, `SELECT `+iamUserCols+` FROM iam_users WHERE account_id=? AND user_name=?`, account, name))
}

func (r *iamRepo) GetUserByID(ctx context.Context, id int64) (*IAMUser, error) {
	return scanIAMUser(r.d.QueryRowContext(ctx, `SELECT `+iamUserCols+` FROM iam_users WHERE id=?`, id))
}

func (r *iamRepo) ListUsers(ctx context.Context, account string) ([]IAMUser, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+iamUserCols+` FROM iam_users WHERE account_id=? ORDER BY user_name`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMUser{}
	for rows.Next() {
		u, err := scanIAMUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (r *iamRepo) DeleteUser(ctx context.Context, id int64) error {
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM iam_policy_attachments WHERE principal_type='user' AND principal_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM iam_inline_policies WHERE principal_type='user' AND principal_id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM iam_users WHERE id=?`, id)
		return err
	})
}

func (r *iamRepo) CreateGroup(ctx context.Context, g *IAMGroup) error {
	g.CreatedAt = db.Now()
	res, err := r.d.ExecContext(ctx, `INSERT INTO iam_groups(account_id,group_name,group_id,path,created_at) VALUES(?,?,?,?,?)`,
		g.AccountID, g.GroupName, g.GroupID, g.Path, g.CreatedAt)
	if err != nil {
		return err
	}
	g.ID, _ = res.LastInsertId()
	g.ARN = "arn:cloudsim:iam::" + g.AccountID + ":group" + g.Path + g.GroupName
	return nil
}

func scanGroup(row interface{ Scan(...any) error }) (*IAMGroup, error) {
	var g IAMGroup
	if err := row.Scan(&g.ID, &g.AccountID, &g.GroupName, &g.GroupID, &g.Path, &g.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	g.ARN = "arn:cloudsim:iam::" + g.AccountID + ":group" + g.Path + g.GroupName
	return &g, nil
}

func (r *iamRepo) groups(ctx context.Context, q string, args ...any) ([]IAMGroup, error) {
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMGroup{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (r *iamRepo) GetGroup(ctx context.Context, account, name string) (*IAMGroup, error) {
	return scanGroup(r.d.QueryRowContext(ctx, `SELECT id,account_id,group_name,group_id,path,created_at FROM iam_groups WHERE account_id=? AND group_name=?`, account, name))
}

func (r *iamRepo) ListGroups(ctx context.Context, account string) ([]IAMGroup, error) {
	return r.groups(ctx, `SELECT id,account_id,group_name,group_id,path,created_at FROM iam_groups WHERE account_id=? ORDER BY group_name`, account)
}

func (r *iamRepo) GroupsForUser(ctx context.Context, userID int64) ([]IAMGroup, error) {
	return r.groups(ctx, `SELECT g.id,g.account_id,g.group_name,g.group_id,g.path,g.created_at FROM iam_groups g JOIN iam_group_members m ON m.group_id=g.id WHERE m.user_id=? ORDER BY g.group_name`, userID)
}

func (r *iamRepo) GroupMembers(ctx context.Context, groupID int64) ([]IAMUser, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT u.id,u.account_id,u.user_name,u.user_id,u.path,u.tags,u.created_at FROM iam_users u JOIN iam_group_members m ON m.user_id=u.id WHERE m.group_id=? ORDER BY u.user_name`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMUser{}
	for rows.Next() {
		u, err := scanIAMUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (r *iamRepo) DeleteGroup(ctx context.Context, id int64) error {
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM iam_policy_attachments WHERE principal_type='group' AND principal_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM iam_inline_policies WHERE principal_type='group' AND principal_id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM iam_groups WHERE id=?`, id)
		return err
	})
}

func (r *iamRepo) AddMember(ctx context.Context, groupID, userID int64) error {
	_, err := r.d.ExecContext(ctx, `INSERT OR IGNORE INTO iam_group_members(group_id,user_id) VALUES(?,?)`, groupID, userID)
	return err
}

func (r *iamRepo) RemoveMember(ctx context.Context, groupID, userID int64) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM iam_group_members WHERE group_id=? AND user_id=?`, groupID, userID)
	return err
}

const roleCols = `id,account_id,role_name,role_id,path,description,assume_role_policy,max_session_duration,created_at`

func scanRole(row interface{ Scan(...any) error }) (*IAMRole, error) {
	var x IAMRole
	var doc string
	if err := row.Scan(&x.ID, &x.AccountID, &x.RoleName, &x.RoleID, &x.Path, &x.Description, &doc, &x.MaxSessionDuration, &x.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	x.AssumeRolePolicy = json.RawMessage(doc)
	x.ARN = "arn:cloudsim:iam::" + x.AccountID + ":role" + x.Path + x.RoleName
	return &x, nil
}

func (r *iamRepo) CreateRole(ctx context.Context, x *IAMRole) error {
	x.CreatedAt = db.Now()
	res, err := r.d.ExecContext(ctx, `INSERT INTO iam_roles(account_id,role_name,role_id,path,description,assume_role_policy,max_session_duration,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		x.AccountID, x.RoleName, x.RoleID, x.Path, x.Description, string(x.AssumeRolePolicy), x.MaxSessionDuration, x.CreatedAt)
	if err != nil {
		return err
	}
	x.ID, _ = res.LastInsertId()
	x.ARN = "arn:cloudsim:iam::" + x.AccountID + ":role" + x.Path + x.RoleName
	return nil
}

func (r *iamRepo) GetRole(ctx context.Context, account, name string) (*IAMRole, error) {
	return scanRole(r.d.QueryRowContext(ctx, `SELECT `+roleCols+` FROM iam_roles WHERE account_id=? AND role_name=?`, account, name))
}

func (r *iamRepo) GetRoleByID(ctx context.Context, id int64) (*IAMRole, error) {
	return scanRole(r.d.QueryRowContext(ctx, `SELECT `+roleCols+` FROM iam_roles WHERE id=?`, id))
}

func (r *iamRepo) ListRoles(ctx context.Context, account string) ([]IAMRole, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+roleCols+` FROM iam_roles WHERE account_id=? ORDER BY role_name`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMRole{}
	for rows.Next() {
		x, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

func (r *iamRepo) DeleteRole(ctx context.Context, id int64) error {
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM iam_policy_attachments WHERE principal_type='role' AND principal_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM iam_inline_policies WHERE principal_type='role' AND principal_id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM iam_roles WHERE id=?`, id)
		return err
	})
}

const policyCols = `p.id,p.account_id,p.policy_name,p.policy_id,p.arn,p.description,p.document,p.version,p.builtin,p.created_at,p.updated_at,
 (SELECT COUNT(*) FROM iam_policy_attachments a WHERE a.policy_id=p.id)`

func scanPolicy(row interface{ Scan(...any) error }) (*IAMPolicy, error) {
	var p IAMPolicy
	var doc string
	var builtin int
	if err := row.Scan(&p.ID, &p.AccountID, &p.PolicyName, &p.PolicyID, &p.ARN, &p.Description, &doc, &p.Version, &builtin, &p.CreatedAt, &p.UpdatedAt, &p.Attachments); err != nil {
		return nil, notFound(err)
	}
	p.Document = json.RawMessage(doc)
	p.Builtin = builtin == 1
	return &p, nil
}

func (r *iamRepo) UpsertBuiltinPolicy(ctx context.Context, p *IAMPolicy) error {
	now := db.Now()
	_, err := r.d.ExecContext(ctx, `INSERT INTO iam_policies(account_id,policy_name,policy_id,arn,description,document,version,builtin,created_at,updated_at)
		VALUES('',?,?,?,?,?,1,1,?,?)
		ON CONFLICT(account_id,policy_name) DO UPDATE SET document=excluded.document, description=excluded.description, updated_at=excluded.updated_at`,
		p.PolicyName, p.PolicyID, p.ARN, p.Description, string(p.Document), now, now)
	return err
}

func (r *iamRepo) CreatePolicy(ctx context.Context, p *IAMPolicy) error {
	now := db.Now()
	res, err := r.d.ExecContext(ctx, `INSERT INTO iam_policies(account_id,policy_name,policy_id,arn,description,document,version,builtin,created_at,updated_at) VALUES(?,?,?,?,?,?,1,0,?,?)`,
		p.AccountID, p.PolicyName, p.PolicyID, p.ARN, p.Description, string(p.Document), now, now)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	p.Version, p.CreatedAt, p.UpdatedAt = 1, now, now
	return nil
}

// GetPolicy finds an account policy or a builtin one by name or ARN.
func (r *iamRepo) GetPolicy(ctx context.Context, account, nameOrARN string) (*IAMPolicy, error) {
	return scanPolicy(r.d.QueryRowContext(ctx, `SELECT `+policyCols+` FROM iam_policies p WHERE (p.account_id=? OR p.account_id='') AND (p.policy_name=? OR p.arn=?) ORDER BY p.account_id DESC LIMIT 1`,
		account, nameOrARN, nameOrARN))
}

func (r *iamRepo) ListPolicies(ctx context.Context, account string) ([]IAMPolicy, error) {
	return r.policies(ctx, `SELECT `+policyCols+` FROM iam_policies p WHERE p.account_id=? OR p.account_id='' ORDER BY p.builtin, p.policy_name`, account)
}

func (r *iamRepo) policies(ctx context.Context, q string, args ...any) ([]IAMPolicy, error) {
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMPolicy{}
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *iamRepo) UpdatePolicyDocument(ctx context.Context, id int64, doc string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE iam_policies SET document=?, version=version+1, updated_at=? WHERE id=?`, doc, db.Now(), id)
	return err
}

func (r *iamRepo) DeletePolicy(ctx context.Context, id int64) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM iam_policies WHERE id=?`, id)
	return err
}

func (r *iamRepo) Attach(ctx context.Context, policyID int64, account, ptype string, pid int64) error {
	_, err := r.d.ExecContext(ctx, `INSERT OR IGNORE INTO iam_policy_attachments(policy_id,account_id,principal_type,principal_id) VALUES(?,?,?,?)`, policyID, account, ptype, pid)
	return err
}

func (r *iamRepo) Detach(ctx context.Context, policyID int64, ptype string, pid int64) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM iam_policy_attachments WHERE policy_id=? AND principal_type=? AND principal_id=?`, policyID, ptype, pid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *iamRepo) AttachedPolicies(ctx context.Context, ptype string, pid int64) ([]IAMPolicy, error) {
	return r.policies(ctx, `SELECT `+policyCols+` FROM iam_policies p JOIN iam_policy_attachments a ON a.policy_id=p.id WHERE a.principal_type=? AND a.principal_id=? ORDER BY p.policy_name`, ptype, pid)
}

func (r *iamRepo) PutInline(ctx context.Context, account, ptype string, pid int64, name, doc string) error {
	_, err := r.d.ExecContext(ctx, `INSERT INTO iam_inline_policies(account_id,principal_type,principal_id,policy_name,document) VALUES(?,?,?,?,?)
		ON CONFLICT(principal_type,principal_id,policy_name) DO UPDATE SET document=excluded.document`, account, ptype, pid, name, doc)
	return err
}

func (r *iamRepo) DeleteInline(ctx context.Context, ptype string, pid int64, name string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM iam_inline_policies WHERE principal_type=? AND principal_id=? AND policy_name=?`, ptype, pid, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *iamRepo) InlinePolicies(ctx context.Context, ptype string, pid int64) ([]InlinePolicy, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT policy_name, document FROM iam_inline_policies WHERE principal_type=? AND principal_id=? ORDER BY policy_name`, ptype, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InlinePolicy{}
	for rows.Next() {
		var p InlinePolicy
		var doc string
		if err := rows.Scan(&p.PolicyName, &doc); err != nil {
			return nil, err
		}
		p.Document = json.RawMessage(doc)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *iamRepo) DeletePrincipalPolicies(ctx context.Context, ptype string, pid int64) error {
	if _, err := r.d.ExecContext(ctx, `DELETE FROM iam_policy_attachments WHERE principal_type=? AND principal_id=?`, ptype, pid); err != nil {
		return err
	}
	_, err := r.d.ExecContext(ctx, `DELETE FROM iam_inline_policies WHERE principal_type=? AND principal_id=?`, ptype, pid)
	return err
}

func (r *iamRepo) EffectivePolicies(ctx context.Context, ptype string, pid int64) ([]PolicyRow, error) {
	var q string
	var args []any
	if ptype == PUser {
		q = `SELECT p.policy_name, p.document FROM iam_policies p JOIN iam_policy_attachments a ON a.policy_id=p.id WHERE a.principal_type='user' AND a.principal_id=?1
		UNION ALL SELECT 'inline:'||policy_name, document FROM iam_inline_policies WHERE principal_type='user' AND principal_id=?1
		UNION ALL SELECT p.policy_name, p.document FROM iam_policies p JOIN iam_policy_attachments a ON a.policy_id=p.id JOIN iam_group_members m ON a.principal_type='group' AND a.principal_id=m.group_id WHERE m.user_id=?1
		UNION ALL SELECT 'inline:'||i.policy_name, i.document FROM iam_inline_policies i JOIN iam_group_members m ON i.principal_type='group' AND i.principal_id=m.group_id WHERE m.user_id=?1`
		args = []any{pid}
	} else {
		q = `SELECT p.policy_name, p.document FROM iam_policies p JOIN iam_policy_attachments a ON a.policy_id=p.id WHERE a.principal_type=?1 AND a.principal_id=?2
		UNION ALL SELECT 'inline:'||policy_name, document FROM iam_inline_policies WHERE principal_type=?1 AND principal_id=?2`
		args = []any{ptype, pid}
	}
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRow
	for rows.Next() {
		var p PolicyRow
		if err := rows.Scan(&p.Name, &p.Document); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const keyCols = `access_key_id,account_id,iam_user_id,role_id,session_name,secret_hash,session_token_hash,status,expires_at,last_used_at,created_at`

func scanKey(row interface{ Scan(...any) error }) (*AccessKey, error) {
	var k AccessKey
	var uid, rid sql.NullInt64
	var exp, last sql.NullString
	if err := row.Scan(&k.AccessKeyID, &k.AccountID, &uid, &rid, &k.SessionName, &k.SecretHash, &k.SessionTokenHash, &k.Status, &exp, &last, &k.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	if uid.Valid {
		k.IAMUserID = &uid.Int64
	}
	if rid.Valid {
		k.RoleID = &rid.Int64
	}
	if exp.Valid {
		k.ExpiresAt = &exp.String
	}
	if last.Valid {
		k.LastUsedAt = &last.String
	}
	return &k, nil
}

func (r *iamRepo) CreateAccessKey(ctx context.Context, k *AccessKey) error {
	k.CreatedAt = db.Now()
	_, err := r.d.ExecContext(ctx, `INSERT INTO iam_access_keys(`+keyCols+`) VALUES(?,?,?,?,?,?,?,?,?,NULL,?)`,
		k.AccessKeyID, k.AccountID, k.IAMUserID, k.RoleID, k.SessionName, k.SecretHash, k.SessionTokenHash, k.Status, k.ExpiresAt, k.CreatedAt)
	return err
}

func (r *iamRepo) GetAccessKey(ctx context.Context, id string) (*AccessKey, error) {
	return scanKey(r.d.QueryRowContext(ctx, `SELECT `+keyCols+` FROM iam_access_keys WHERE access_key_id=?`, id))
}

func (r *iamRepo) ListAccessKeys(ctx context.Context, iamUserID int64) ([]AccessKey, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+keyCols+` FROM iam_access_keys WHERE iam_user_id=? ORDER BY created_at`, iamUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessKey{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func (r *iamRepo) SetAccessKeyStatus(ctx context.Context, id, status string) error {
	res, err := r.d.ExecContext(ctx, `UPDATE iam_access_keys SET status=? WHERE access_key_id=?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *iamRepo) DeleteAccessKey(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM iam_access_keys WHERE access_key_id=?`, id)
	return err
}

func (r *iamRepo) TouchAccessKey(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE iam_access_keys SET last_used_at=? WHERE access_key_id=?`, db.Now(), id)
	return err
}

func (r *iamRepo) DeleteExpiredKeys(ctx context.Context) (int64, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM iam_access_keys WHERE expires_at IS NOT NULL AND expires_at < ?`, db.Now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

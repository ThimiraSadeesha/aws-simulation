package repositories

import (
	"context"
	"database/sql"
	"encoding/json"

	"cloudsim/backend/internal/db"
)

type LaunchTemplate struct {
	LaunchTemplateID string   `json:"launch_template_id"`
	AccountID        string   `json:"-"`
	Name             string   `json:"name"`
	ImageID          string   `json:"image_id"`
	InstanceType     string   `json:"instance_type"`
	SecurityGroupIDs []string `json:"security_group_ids"`
	KeyName          string   `json:"key_name"`
	UserData         string   `json:"user_data"`
	WorkloadProfile  string   `json:"workload_profile"`
	CreatedAt        string   `json:"created_at"`
}

type ASGroup struct {
	GroupID            string   `json:"group_id"`
	AccountID          string   `json:"-"`
	Name               string   `json:"name"`
	LaunchTemplateID   string   `json:"-"`
	LaunchTemplate     string   `json:"launch_template"`
	VPCID              string   `json:"vpc_id"`
	MinSize            int      `json:"min_size"`
	MaxSize            int      `json:"max_size"`
	DesiredCapacity    int      `json:"desired_capacity"`
	CooldownS          int      `json:"cooldown_s"`
	HealthCheckType    string   `json:"health_check_type"`
	HealthCheckGraceS  int      `json:"health_check_grace_period_s"`
	LastScalingAt      *string  `json:"last_scaling_at"`
	SubnetIDs          []string `json:"subnet_ids"`
	TargetGroupIDs     []string `json:"target_group_ids"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
}

type ScalingPolicy struct {
	PolicyID    string   `json:"policy_id"`
	GroupID     string   `json:"-"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	TargetValue *float64 `json:"target_value,omitempty"`
	Threshold   *float64 `json:"threshold,omitempty"`
	Comparison  *string  `json:"comparison,omitempty"`
	Adjustment  *int     `json:"adjustment,omitempty"`
	ARN         string   `json:"arn"`
	CreatedAt   string   `json:"created_at"`
}

type ScalingActivity struct {
	ID          int64  `json:"id"`
	GroupID     string `json:"-"`
	AccountID   string `json:"-"`
	TS          string `json:"ts"`
	Description string `json:"description"`
	Cause       string `json:"cause"`
	Status      string `json:"status"`
}

// AutoScalingRepo persists launch templates, groups, policies and activities.
type AutoScalingRepo interface {
	InsertTemplate(ctx context.Context, t *LaunchTemplate) error
	GetTemplate(ctx context.Context, account, nameOrID string) (*LaunchTemplate, error)
	GetTemplateByID(ctx context.Context, id string) (*LaunchTemplate, error)
	ListTemplates(ctx context.Context, account string) ([]LaunchTemplate, error)
	DeleteTemplate(ctx context.Context, id string) error
	TemplateInUse(ctx context.Context, id string) (int, error)

	InsertGroup(ctx context.Context, g *ASGroup) error
	GetGroup(ctx context.Context, account, name string) (*ASGroup, error)
	ListGroups(ctx context.Context, account string) ([]ASGroup, error) // account "" = all
	UpdateCapacity(ctx context.Context, groupID string, min, max, desired int) error
	SetDesired(ctx context.Context, groupID string, desired int, scaling bool) error
	DeleteGroup(ctx context.Context, groupID string) error

	InsertPolicy(ctx context.Context, p *ScalingPolicy) error
	ListPolicies(ctx context.Context, groupID string) ([]ScalingPolicy, error)
	DeletePolicy(ctx context.Context, groupID, name string) (bool, error)

	InsertActivity(ctx context.Context, a *ScalingActivity) error
	ListActivities(ctx context.Context, groupID string, limit int) ([]ScalingActivity, error)
}

func NewAutoScalingRepo(d *db.DB) AutoScalingRepo { return &asgRepo{d} }

type asgRepo struct{ d *db.DB }

const ltCols = `launch_template_id,account_id,name,image_id,instance_type,security_group_ids,key_name,user_data,workload_profile,created_at`

func scanLT(row interface{ Scan(...any) error }) (*LaunchTemplate, error) {
	var t LaunchTemplate
	var sgs string
	if err := row.Scan(&t.LaunchTemplateID, &t.AccountID, &t.Name, &t.ImageID, &t.InstanceType, &sgs, &t.KeyName, &t.UserData, &t.WorkloadProfile, &t.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(sgs), &t.SecurityGroupIDs)
	if t.SecurityGroupIDs == nil {
		t.SecurityGroupIDs = []string{}
	}
	return &t, nil
}

func (r *asgRepo) InsertTemplate(ctx context.Context, t *LaunchTemplate) error {
	t.CreatedAt = db.Now()
	sgs, _ := json.Marshal(t.SecurityGroupIDs)
	_, err := r.d.ExecContext(ctx, `INSERT INTO asg_launch_templates(`+ltCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		t.LaunchTemplateID, t.AccountID, t.Name, t.ImageID, t.InstanceType, string(sgs), t.KeyName, t.UserData, t.WorkloadProfile, t.CreatedAt)
	return err
}

func (r *asgRepo) GetTemplate(ctx context.Context, account, nameOrID string) (*LaunchTemplate, error) {
	return scanLT(r.d.QueryRowContext(ctx, `SELECT `+ltCols+` FROM asg_launch_templates WHERE account_id=? AND (name=? OR launch_template_id=?)`, account, nameOrID, nameOrID))
}

func (r *asgRepo) GetTemplateByID(ctx context.Context, id string) (*LaunchTemplate, error) {
	return scanLT(r.d.QueryRowContext(ctx, `SELECT `+ltCols+` FROM asg_launch_templates WHERE launch_template_id=?`, id))
}

func (r *asgRepo) ListTemplates(ctx context.Context, account string) ([]LaunchTemplate, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+ltCols+` FROM asg_launch_templates WHERE account_id=? ORDER BY name`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LaunchTemplate{}
	for rows.Next() {
		t, err := scanLT(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *asgRepo) DeleteTemplate(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM asg_launch_templates WHERE launch_template_id=?`, id)
	return err
}

func (r *asgRepo) TemplateInUse(ctx context.Context, id string) (int, error) {
	var n int
	err := r.d.QueryRowContext(ctx, `SELECT COUNT(*) FROM asg_groups WHERE launch_template_id=?`, id).Scan(&n)
	return n, err
}

const grpCols = `g.group_id,g.account_id,g.name,g.launch_template_id,COALESCE(t.name,''),g.vpc_id,g.min_size,g.max_size,g.desired_capacity,g.cooldown_s,
 g.health_check_type,g.health_check_grace_s,g.last_scaling_at,g.created_at,g.updated_at`

func (r *asgRepo) scanGroup(row interface{ Scan(...any) error }) (*ASGroup, error) {
	var g ASGroup
	var last sql.NullString
	if err := row.Scan(&g.GroupID, &g.AccountID, &g.Name, &g.LaunchTemplateID, &g.LaunchTemplate, &g.VPCID, &g.MinSize, &g.MaxSize, &g.DesiredCapacity,
		&g.CooldownS, &g.HealthCheckType, &g.HealthCheckGraceS, &last, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if last.Valid {
		g.LastScalingAt = &last.String
	}
	return &g, nil
}

func (r *asgRepo) strs(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *asgRepo) decorate(ctx context.Context, g *ASGroup) error {
	var err error
	if g.SubnetIDs, err = r.strs(ctx, `SELECT subnet_id FROM asg_group_subnets WHERE group_id=? ORDER BY position`, g.GroupID); err != nil {
		return err
	}
	g.TargetGroupIDs, err = r.strs(ctx, `SELECT target_group_id FROM asg_group_target_groups WHERE group_id=? ORDER BY target_group_id`, g.GroupID)
	return err
}

func (r *asgRepo) InsertGroup(ctx context.Context, g *ASGroup) error {
	now := db.Now()
	g.CreatedAt, g.UpdatedAt = now, now
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO asg_groups(group_id,account_id,name,launch_template_id,vpc_id,min_size,max_size,desired_capacity,cooldown_s,health_check_type,health_check_grace_s,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, g.GroupID, g.AccountID, g.Name, g.LaunchTemplateID, g.VPCID, g.MinSize, g.MaxSize, g.DesiredCapacity,
			g.CooldownS, g.HealthCheckType, g.HealthCheckGraceS, now, now); err != nil {
			return err
		}
		for i, s := range g.SubnetIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO asg_group_subnets(group_id,subnet_id,position) VALUES(?,?,?)`, g.GroupID, s, i); err != nil {
				return err
			}
		}
		for _, t := range g.TargetGroupIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO asg_group_target_groups(group_id,target_group_id) VALUES(?,?)`, g.GroupID, t); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *asgRepo) GetGroup(ctx context.Context, account, name string) (*ASGroup, error) {
	g, err := r.scanGroup(r.d.QueryRowContext(ctx, `SELECT `+grpCols+` FROM asg_groups g LEFT JOIN asg_launch_templates t ON t.launch_template_id=g.launch_template_id
		WHERE g.account_id=? AND (g.name=? OR g.group_id=?)`, account, name, name))
	if err != nil {
		return nil, err
	}
	return g, r.decorate(ctx, g)
}

func (r *asgRepo) ListGroups(ctx context.Context, account string) ([]ASGroup, error) {
	q := `SELECT ` + grpCols + ` FROM asg_groups g LEFT JOIN asg_launch_templates t ON t.launch_template_id=g.launch_template_id`
	var args []any
	if account != "" {
		q += ` WHERE g.account_id=?`
		args = append(args, account)
	}
	rows, err := r.d.QueryContext(ctx, q+` ORDER BY g.created_at`, args...)
	if err != nil {
		return nil, err
	}
	out := []ASGroup{}
	for rows.Next() {
		g, err := r.scanGroup(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *g)
	}
	rows.Close()
	for i := range out {
		if err := r.decorate(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *asgRepo) UpdateCapacity(ctx context.Context, groupID string, min, max, desired int) error {
	_, err := r.d.ExecContext(ctx, `UPDATE asg_groups SET min_size=?, max_size=?, desired_capacity=?, updated_at=? WHERE group_id=?`, min, max, desired, db.Now(), groupID)
	return err
}

func (r *asgRepo) SetDesired(ctx context.Context, groupID string, desired int, scaling bool) error {
	now := db.Now()
	if scaling {
		_, err := r.d.ExecContext(ctx, `UPDATE asg_groups SET desired_capacity=?, last_scaling_at=?, updated_at=? WHERE group_id=?`, desired, now, now, groupID)
		return err
	}
	_, err := r.d.ExecContext(ctx, `UPDATE asg_groups SET desired_capacity=?, updated_at=? WHERE group_id=?`, desired, now, groupID)
	return err
}

func (r *asgRepo) DeleteGroup(ctx context.Context, groupID string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM asg_groups WHERE group_id=?`, groupID)
	return err
}

func (r *asgRepo) InsertPolicy(ctx context.Context, p *ScalingPolicy) error {
	p.CreatedAt = db.Now()
	_, err := r.d.ExecContext(ctx, `INSERT INTO asg_policies(policy_id,group_id,name,type,target_value,threshold,comparison,adjustment,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		p.PolicyID, p.GroupID, p.Name, p.Type, p.TargetValue, p.Threshold, p.Comparison, p.Adjustment, p.CreatedAt)
	return err
}

func (r *asgRepo) ListPolicies(ctx context.Context, groupID string) ([]ScalingPolicy, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT policy_id,group_id,name,type,target_value,threshold,comparison,adjustment,created_at FROM asg_policies WHERE group_id=? ORDER BY name`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScalingPolicy{}
	for rows.Next() {
		var p ScalingPolicy
		var tv, th sql.NullFloat64
		var cmp sql.NullString
		var adj sql.NullInt64
		if err := rows.Scan(&p.PolicyID, &p.GroupID, &p.Name, &p.Type, &tv, &th, &cmp, &adj, &p.CreatedAt); err != nil {
			return nil, err
		}
		if tv.Valid {
			p.TargetValue = &tv.Float64
		}
		if th.Valid {
			p.Threshold = &th.Float64
		}
		if cmp.Valid {
			p.Comparison = &cmp.String
		}
		if adj.Valid {
			a := int(adj.Int64)
			p.Adjustment = &a
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *asgRepo) DeletePolicy(ctx context.Context, groupID, name string) (bool, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM asg_policies WHERE group_id=? AND name=?`, groupID, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *asgRepo) InsertActivity(ctx context.Context, a *ScalingActivity) error {
	if a.TS == "" {
		a.TS = db.Now()
	}
	res, err := r.d.ExecContext(ctx, `INSERT INTO asg_activities(group_id,account_id,ts,description,cause,status) VALUES(?,?,?,?,?,?)`,
		a.GroupID, a.AccountID, a.TS, a.Description, a.Cause, a.Status)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

func (r *asgRepo) ListActivities(ctx context.Context, groupID string, limit int) ([]ScalingActivity, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.d.QueryContext(ctx, `SELECT id,group_id,account_id,ts,description,cause,status FROM asg_activities WHERE group_id=? ORDER BY id DESC LIMIT ?`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScalingActivity{}
	for rows.Next() {
		var a ScalingActivity
		if err := rows.Scan(&a.ID, &a.GroupID, &a.AccountID, &a.TS, &a.Description, &a.Cause, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

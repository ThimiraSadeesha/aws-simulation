package repositories

import (
	"context"
	"database/sql"

	"cloudsim/backend/internal/db"
)

type HealthCheck struct {
	Path               string `json:"path"`
	IntervalS          int    `json:"interval_s"`
	HealthyThreshold   int    `json:"healthy_threshold"`
	UnhealthyThreshold int    `json:"unhealthy_threshold"`
}

type TargetGroup struct {
	TargetGroupID string      `json:"target_group_id"`
	AccountID     string      `json:"-"`
	Name          string      `json:"name"`
	Protocol      string      `json:"protocol"`
	Port          int         `json:"port"`
	VPCID         string      `json:"vpc_id"`
	Algorithm     string      `json:"algorithm"`
	HealthCheck   HealthCheck `json:"health_check"`
	CreatedAt     string      `json:"created_at"`
}

type ELBTarget struct {
	TargetGroupID string `json:"-"`
	InstanceID    string `json:"id"`
	Port          int    `json:"port"`
	Weight        int    `json:"weight"`
	RegisteredAt  string `json:"registered_at"`
}

type LoadBalancer struct {
	LoadBalancerID   string   `json:"load_balancer_id"`
	AccountID        string   `json:"-"`
	Name             string   `json:"name"`
	DNSName          string   `json:"dns_name"`
	Scheme           string   `json:"scheme"`
	State            string   `json:"state"`
	VPCID            string   `json:"vpc_id"`
	SubnetIDs        []string `json:"subnet_ids"`
	SecurityGroupIDs []string `json:"security_group_ids"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

type Listener struct {
	ListenerID           string `json:"listener_id"`
	LoadBalancerID       string `json:"load_balancer_id"`
	Protocol             string `json:"protocol"`
	Port                 int    `json:"port"`
	DefaultTargetGroupID string `json:"default_target_group_id"`
	CreatedAt            string `json:"created_at"`
}

// ELBRepo persists ELB resources.
type ELBRepo interface {
	InsertTG(ctx context.Context, tg *TargetGroup) error
	GetTG(ctx context.Context, account, id string) (*TargetGroup, error)
	ListTGs(ctx context.Context, account string) ([]TargetGroup, error)
	ListAllTGs(ctx context.Context) ([]TargetGroup, error)
	UpdateTG(ctx context.Context, tg *TargetGroup) error
	DeleteTG(ctx context.Context, id string) error
	TGListenerCount(ctx context.Context, tgID string) (int, error)
	TGASGCount(ctx context.Context, tgID string) (int, error)

	UpsertTarget(ctx context.Context, t *ELBTarget) error
	DeleteTarget(ctx context.Context, tgID, instanceID string) (bool, error)
	ListTargets(ctx context.Context, tgID string) ([]ELBTarget, error)
	TargetGroupsOfInstance(ctx context.Context, instanceID string) ([]string, error)
	DeleteTargetsOfInstance(ctx context.Context, instanceID string) (int64, error)

	InsertLB(ctx context.Context, lb *LoadBalancer) error
	GetLB(ctx context.Context, account, id string) (*LoadBalancer, error)
	GetLBAny(ctx context.Context, id string) (*LoadBalancer, error)
	ListLBs(ctx context.Context, account string) ([]LoadBalancer, error)
	ListAllLBs(ctx context.Context) ([]LoadBalancer, error)
	SetLBState(ctx context.Context, id, state string) error
	DeleteLB(ctx context.Context, id string) error
	LBsOfTG(ctx context.Context, tgID string) ([]string, error)

	InsertListener(ctx context.Context, l *Listener) error
	ListListeners(ctx context.Context, lbID string) ([]Listener, error)
	DeleteListener(ctx context.Context, lbID, listenerID string) (bool, error)
}

func NewELBRepo(d *db.DB) ELBRepo { return &elbRepo{d} }

type elbRepo struct{ d *db.DB }

const tgCols = `target_group_id,account_id,name,protocol,port,vpc_id,algorithm,hc_path,hc_interval_s,hc_healthy_threshold,hc_unhealthy_threshold,created_at`

func scanTG(row interface{ Scan(...any) error }) (*TargetGroup, error) {
	var t TargetGroup
	err := row.Scan(&t.TargetGroupID, &t.AccountID, &t.Name, &t.Protocol, &t.Port, &t.VPCID, &t.Algorithm,
		&t.HealthCheck.Path, &t.HealthCheck.IntervalS, &t.HealthCheck.HealthyThreshold, &t.HealthCheck.UnhealthyThreshold, &t.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

func (r *elbRepo) InsertTG(ctx context.Context, t *TargetGroup) error {
	t.CreatedAt = db.Now()
	_, err := r.d.ExecContext(ctx, `INSERT INTO elb_target_groups(`+tgCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.TargetGroupID, t.AccountID, t.Name, t.Protocol, t.Port, t.VPCID, t.Algorithm,
		t.HealthCheck.Path, t.HealthCheck.IntervalS, t.HealthCheck.HealthyThreshold, t.HealthCheck.UnhealthyThreshold, t.CreatedAt)
	return err
}

func (r *elbRepo) GetTG(ctx context.Context, account, id string) (*TargetGroup, error) {
	return scanTG(r.d.QueryRowContext(ctx, `SELECT `+tgCols+` FROM elb_target_groups WHERE account_id=? AND (target_group_id=? OR name=?)`, account, id, id))
}

func (r *elbRepo) listTGs(ctx context.Context, where string, args ...any) ([]TargetGroup, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+tgCols+` FROM elb_target_groups `+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TargetGroup{}
	for rows.Next() {
		t, err := scanTG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *elbRepo) ListTGs(ctx context.Context, account string) ([]TargetGroup, error) {
	return r.listTGs(ctx, `WHERE account_id=?`, account)
}

func (r *elbRepo) ListAllTGs(ctx context.Context) ([]TargetGroup, error) { return r.listTGs(ctx, ``) }

func (r *elbRepo) UpdateTG(ctx context.Context, t *TargetGroup) error {
	_, err := r.d.ExecContext(ctx, `UPDATE elb_target_groups SET algorithm=?,hc_path=?,hc_interval_s=?,hc_healthy_threshold=?,hc_unhealthy_threshold=? WHERE target_group_id=?`,
		t.Algorithm, t.HealthCheck.Path, t.HealthCheck.IntervalS, t.HealthCheck.HealthyThreshold, t.HealthCheck.UnhealthyThreshold, t.TargetGroupID)
	return err
}

func (r *elbRepo) DeleteTG(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM elb_target_groups WHERE target_group_id=?`, id)
	return err
}

func (r *elbRepo) count(ctx context.Context, q string, args ...any) (int, error) {
	var n int
	err := r.d.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func (r *elbRepo) TGListenerCount(ctx context.Context, tgID string) (int, error) {
	return r.count(ctx, `SELECT COUNT(*) FROM elb_listeners WHERE default_target_group_id=?`, tgID)
}

func (r *elbRepo) TGASGCount(ctx context.Context, tgID string) (int, error) {
	n, err := r.count(ctx, `SELECT COUNT(*) FROM asg_group_target_groups WHERE target_group_id=?`, tgID)
	if err != nil { // autoscaling tables may not exist in isolated tests
		return 0, nil
	}
	return n, nil
}

func (r *elbRepo) UpsertTarget(ctx context.Context, t *ELBTarget) error {
	t.RegisteredAt = db.Now()
	_, err := r.d.ExecContext(ctx, `INSERT INTO elb_targets(target_group_id,instance_id,port,weight,registered_at) VALUES(?,?,?,?,?)
		ON CONFLICT(target_group_id,instance_id) DO UPDATE SET port=excluded.port, weight=excluded.weight`,
		t.TargetGroupID, t.InstanceID, t.Port, t.Weight, t.RegisteredAt)
	return err
}

func (r *elbRepo) DeleteTarget(ctx context.Context, tgID, instanceID string) (bool, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM elb_targets WHERE target_group_id=? AND instance_id=?`, tgID, instanceID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *elbRepo) ListTargets(ctx context.Context, tgID string) ([]ELBTarget, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT target_group_id,instance_id,port,weight,registered_at FROM elb_targets WHERE target_group_id=? ORDER BY registered_at, instance_id`, tgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ELBTarget{}
	for rows.Next() {
		var t ELBTarget
		if err := rows.Scan(&t.TargetGroupID, &t.InstanceID, &t.Port, &t.Weight, &t.RegisteredAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *elbRepo) strings(ctx context.Context, q string, args ...any) ([]string, error) {
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

func (r *elbRepo) TargetGroupsOfInstance(ctx context.Context, instanceID string) ([]string, error) {
	return r.strings(ctx, `SELECT target_group_id FROM elb_targets WHERE instance_id=?`, instanceID)
}

func (r *elbRepo) DeleteTargetsOfInstance(ctx context.Context, instanceID string) (int64, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM elb_targets WHERE instance_id=?`, instanceID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const lbCols = `load_balancer_id,account_id,name,dns_name,scheme,state,vpc_id,created_at,updated_at`

func (r *elbRepo) scanLB(ctx context.Context, row interface{ Scan(...any) error }) (*LoadBalancer, error) {
	var l LoadBalancer
	if err := row.Scan(&l.LoadBalancerID, &l.AccountID, &l.Name, &l.DNSName, &l.Scheme, &l.State, &l.VPCID, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

func (r *elbRepo) decorate(ctx context.Context, l *LoadBalancer) error {
	var err error
	if l.SubnetIDs, err = r.strings(ctx, `SELECT subnet_id FROM elb_lb_subnets WHERE load_balancer_id=? ORDER BY subnet_id`, l.LoadBalancerID); err != nil {
		return err
	}
	l.SecurityGroupIDs, err = r.strings(ctx, `SELECT group_id FROM elb_lb_security_groups WHERE load_balancer_id=? ORDER BY group_id`, l.LoadBalancerID)
	return err
}

func (r *elbRepo) InsertLB(ctx context.Context, l *LoadBalancer) error {
	now := db.Now()
	l.CreatedAt, l.UpdatedAt = now, now
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO elb_load_balancers(`+lbCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
			l.LoadBalancerID, l.AccountID, l.Name, l.DNSName, l.Scheme, l.State, l.VPCID, now, now); err != nil {
			return err
		}
		for _, s := range l.SubnetIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO elb_lb_subnets(load_balancer_id,subnet_id) VALUES(?,?)`, l.LoadBalancerID, s); err != nil {
				return err
			}
		}
		for _, g := range l.SecurityGroupIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO elb_lb_security_groups(load_balancer_id,group_id) VALUES(?,?)`, l.LoadBalancerID, g); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *elbRepo) getLB(ctx context.Context, where string, args ...any) (*LoadBalancer, error) {
	l, err := r.scanLB(ctx, r.d.QueryRowContext(ctx, `SELECT `+lbCols+` FROM elb_load_balancers WHERE `+where, args...))
	if err != nil {
		return nil, err
	}
	return l, r.decorate(ctx, l)
}

func (r *elbRepo) GetLB(ctx context.Context, account, id string) (*LoadBalancer, error) {
	return r.getLB(ctx, `account_id=? AND (load_balancer_id=? OR name=?)`, account, id, id)
}

func (r *elbRepo) GetLBAny(ctx context.Context, id string) (*LoadBalancer, error) {
	return r.getLB(ctx, `load_balancer_id=?`, id)
}

func (r *elbRepo) listLBs(ctx context.Context, where string, args ...any) ([]LoadBalancer, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+lbCols+` FROM elb_load_balancers `+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	out := []LoadBalancer{}
	for rows.Next() {
		l, err := r.scanLB(ctx, rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *l)
	}
	rows.Close()
	for i := range out {
		if err := r.decorate(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *elbRepo) ListLBs(ctx context.Context, account string) ([]LoadBalancer, error) {
	return r.listLBs(ctx, `WHERE account_id=?`, account)
}

func (r *elbRepo) ListAllLBs(ctx context.Context) ([]LoadBalancer, error) { return r.listLBs(ctx, ``) }

func (r *elbRepo) SetLBState(ctx context.Context, id, state string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE elb_load_balancers SET state=?, updated_at=? WHERE load_balancer_id=?`, state, db.Now(), id)
	return err
}

func (r *elbRepo) DeleteLB(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM elb_load_balancers WHERE load_balancer_id=?`, id)
	return err
}

func (r *elbRepo) LBsOfTG(ctx context.Context, tgID string) ([]string, error) {
	return r.strings(ctx, `SELECT DISTINCT l.load_balancer_id FROM elb_listeners l JOIN elb_load_balancers b ON b.load_balancer_id=l.load_balancer_id
		WHERE l.default_target_group_id=? AND b.state IN ('provisioning','active')`, tgID)
}

func (r *elbRepo) InsertListener(ctx context.Context, l *Listener) error {
	l.CreatedAt = db.Now()
	_, err := r.d.ExecContext(ctx, `INSERT INTO elb_listeners(listener_id,load_balancer_id,protocol,port,default_target_group_id,created_at) VALUES(?,?,?,?,?,?)`,
		l.ListenerID, l.LoadBalancerID, l.Protocol, l.Port, l.DefaultTargetGroupID, l.CreatedAt)
	return err
}

func (r *elbRepo) ListListeners(ctx context.Context, lbID string) ([]Listener, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT listener_id,load_balancer_id,protocol,port,default_target_group_id,created_at FROM elb_listeners WHERE load_balancer_id=? ORDER BY port`, lbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Listener{}
	for rows.Next() {
		var l Listener
		if err := rows.Scan(&l.ListenerID, &l.LoadBalancerID, &l.Protocol, &l.Port, &l.DefaultTargetGroupID, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *elbRepo) DeleteListener(ctx context.Context, lbID, listenerID string) (bool, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM elb_listeners WHERE load_balancer_id=? AND listener_id=?`, lbID, listenerID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

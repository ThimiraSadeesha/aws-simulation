package repositories

import (
	"context"
	"database/sql"

	"cloudsim/backend/internal/db"
)

type Instance struct {
	InstanceID       string            `json:"instance_id"`
	AccountID        string            `json:"-"`
	Name             string            `json:"name"`
	State            string            `json:"state"`
	StateReason      string            `json:"state_reason"`
	InstanceType     string            `json:"instance_type"`
	VCPU             int               `json:"vcpu"`
	MemoryMiB        int               `json:"memory_mib"`
	ImageID          string            `json:"image_id"`
	KeyName          string            `json:"key_name"`
	VPCID            string            `json:"vpc_id"`
	SubnetID         string            `json:"subnet_id"`
	PrivateIP        string            `json:"private_ip"`
	PublicIP         string            `json:"public_ip"`
	PrivateDNS       string            `json:"private_dns"`
	SecurityGroupIDs []string          `json:"security_group_ids"`
	Tags             map[string]string `json:"tags"`
	UserData         string            `json:"user_data"`
	BootResult       string            `json:"-"`
	WorkloadProfile  string            `json:"workload_profile"`
	CPUTarget        *float64          `json:"cpu_target,omitempty"`
	AutoScalingGroup string            `json:"autoscaling_group"`
	LaunchTime       string            `json:"launch_time"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
	TerminatedAt     *string           `json:"terminated_at,omitempty"`
}

type KeyPair struct {
	KeyPairID   string `json:"key_pair_id"`
	AccountID   string `json:"-"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
	CreatedAt   string `json:"created_at"`
}

type InstanceFilter struct {
	AccountID string // "" = all accounts (internal use only)
	State     string
	ASG       string
	SubnetID  string
	VPCID     string
	IDs       []string
}

type EC2Repo interface {
	InsertInstance(ctx context.Context, q Querier, i *Instance) error
	GetInstance(ctx context.Context, account, id string) (*Instance, error)
	GetInstanceAny(ctx context.Context, id string) (*Instance, error)
	ListInstances(ctx context.Context, f InstanceFilter) ([]Instance, error)
	UpdateState(ctx context.Context, id, state, reason string) error
	UpdateWorkload(ctx context.Context, id, profile string, cpuTarget *float64) error
	SetPublicIP(ctx context.Context, id, ip string) error
	SetBootResult(ctx context.Context, id, result string) error
	PurgeTerminated(ctx context.Context, olderThan string) (int64, error)
	CountActiveInSubnet(ctx context.Context, subnetID string) (int, error)
	CountActiveInVPC(ctx context.Context, vpcID string) (int, error)

	InsertKeyPair(ctx context.Context, k *KeyPair) error
	ListKeyPairs(ctx context.Context, account string) ([]KeyPair, error)
	GetKeyPair(ctx context.Context, account, name string) (*KeyPair, error)
	DeleteKeyPair(ctx context.Context, account, name string) error
}

type ec2Repo struct{ d *db.DB }

func (r *ec2Repo) InsertInstance(ctx context.Context, q Querier, i *Instance) error {
	if q == nil {
		q = r.d
	}
	now := db.Now()
	i.CreatedAt, i.UpdatedAt, i.LaunchTime = now, now, now
	_, err := q.ExecContext(ctx, `INSERT INTO instances(instance_id,account_id,instance_type,vcpu,memory_mib,image_id,key_name,vpc_id,subnet_id,private_ip,public_ip,state,state_reason,user_data,boot_result,workload_profile,cpu_target,autoscaling_group,launch_time,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.InstanceID, i.AccountID, i.InstanceType, i.VCPU, i.MemoryMiB, i.ImageID, i.KeyName, i.VPCID, i.SubnetID, i.PrivateIP, i.PublicIP,
		i.State, i.StateReason, i.UserData, nz(i.BootResult), i.WorkloadProfile, i.CPUTarget, i.AutoScalingGroup, now, now, now)
	return err
}

const instSelect = `SELECT i.instance_id,i.account_id,i.instance_type,i.vcpu,i.memory_mib,i.image_id,i.key_name,i.vpc_id,i.subnet_id,i.private_ip,i.public_ip,
 i.state,i.state_reason,i.user_data,i.boot_result,i.workload_profile,i.cpu_target,i.autoscaling_group,i.launch_time,i.created_at,i.updated_at,i.terminated_at,
 COALESCE((SELECT group_concat(es.group_id) FROM network_interfaces n JOIN eni_security_groups es ON es.network_interface_id=n.network_interface_id WHERE n.instance_id=i.instance_id AND n.device_index=0),''),
 COALESCE((SELECT value FROM resource_tags t WHERE t.resource_id=i.instance_id AND t.key='Name'),'')
 FROM instances i`

func scanInstance(row interface{ Scan(...any) error }) (*Instance, error) {
	var i Instance
	var cpu sql.NullFloat64
	var term sql.NullString
	var groups string
	if err := row.Scan(&i.InstanceID, &i.AccountID, &i.InstanceType, &i.VCPU, &i.MemoryMiB, &i.ImageID, &i.KeyName, &i.VPCID, &i.SubnetID, &i.PrivateIP, &i.PublicIP,
		&i.State, &i.StateReason, &i.UserData, &i.BootResult, &i.WorkloadProfile, &cpu, &i.AutoScalingGroup, &i.LaunchTime, &i.CreatedAt, &i.UpdatedAt, &term, &groups, &i.Name); err != nil {
		return nil, notFound(err)
	}
	if cpu.Valid {
		i.CPUTarget = &cpu.Float64
	}
	if term.Valid {
		i.TerminatedAt = &term.String
	}
	i.SecurityGroupIDs = splitCSV(groups)
	i.PrivateDNS = PrivateDNS(i.PrivateIP)
	return &i, nil
}

// PrivateDNS returns the simulated internal hostname for an IPv4 address.
func PrivateDNS(ip string) string {
	b := []byte("ip-" + ip + ".cloudsim.internal")
	for k := 3; k < len(b); k++ {
		if b[k] == '.' && k < len(b)-len(".cloudsim.internal") {
			b[k] = '-'
		}
	}
	return string(b)
}

func (r *ec2Repo) GetInstance(ctx context.Context, account, id string) (*Instance, error) {
	return scanInstance(r.d.QueryRowContext(ctx, instSelect+` WHERE i.instance_id=? AND i.account_id=?`, id, account))
}

func (r *ec2Repo) GetInstanceAny(ctx context.Context, id string) (*Instance, error) {
	return scanInstance(r.d.QueryRowContext(ctx, instSelect+` WHERE i.instance_id=?`, id))
}

func (r *ec2Repo) ListInstances(ctx context.Context, f InstanceFilter) ([]Instance, error) {
	q := instSelect + ` WHERE 1=1`
	var args []any
	if f.AccountID != "" {
		q += ` AND i.account_id=?`
		args = append(args, f.AccountID)
	}
	if f.State != "" {
		q += ` AND i.state=?`
		args = append(args, f.State)
	}
	if f.ASG != "" {
		q += ` AND i.autoscaling_group=?`
		args = append(args, f.ASG)
	}
	if f.SubnetID != "" {
		q += ` AND i.subnet_id=?`
		args = append(args, f.SubnetID)
	}
	if f.VPCID != "" {
		q += ` AND i.vpc_id=?`
		args = append(args, f.VPCID)
	}
	if len(f.IDs) > 0 {
		q += ` AND i.instance_id IN (?` + repeatComma(len(f.IDs)-1) + `)`
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY i.created_at DESC`
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Instance{}
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

func repeatComma(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += ",?"
	}
	return s
}

func (r *ec2Repo) UpdateState(ctx context.Context, id, state, reason string) error {
	now := db.Now()
	var term any
	if state == "terminated" {
		term = now
	}
	_, err := r.d.ExecContext(ctx, `UPDATE instances SET state=?, state_reason=?, updated_at=?, terminated_at=COALESCE(?, terminated_at) WHERE instance_id=?`, state, reason, now, term, id)
	return err
}

func (r *ec2Repo) UpdateWorkload(ctx context.Context, id, profile string, cpuTarget *float64) error {
	_, err := r.d.ExecContext(ctx, `UPDATE instances SET workload_profile=?, cpu_target=?, updated_at=? WHERE instance_id=?`, profile, cpuTarget, db.Now(), id)
	return err
}

func (r *ec2Repo) SetPublicIP(ctx context.Context, id, ip string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE instances SET public_ip=? WHERE instance_id=?`, ip, id)
	return err
}

func (r *ec2Repo) PurgeTerminated(ctx context.Context, olderThan string) (int64, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM instances WHERE state='terminated' AND terminated_at < ?`, olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *ec2Repo) CountActiveInSubnet(ctx context.Context, subnetID string) (int, error) {
	var n int
	err := r.d.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances WHERE subnet_id=? AND state<>'terminated'`, subnetID).Scan(&n)
	return n, err
}

func (r *ec2Repo) CountActiveInVPC(ctx context.Context, vpcID string) (int, error) {
	var n int
	err := r.d.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances WHERE vpc_id=? AND state<>'terminated'`, vpcID).Scan(&n)
	return n, err
}

func (r *ec2Repo) InsertKeyPair(ctx context.Context, k *KeyPair) error {
	k.CreatedAt = db.Now()
	_, err := r.d.ExecContext(ctx, `INSERT INTO key_pairs(key_pair_id,account_id,name,fingerprint,public_key,created_at) VALUES(?,?,?,?,?,?)`,
		k.KeyPairID, k.AccountID, k.Name, k.Fingerprint, k.PublicKey, k.CreatedAt)
	return err
}

func (r *ec2Repo) ListKeyPairs(ctx context.Context, account string) ([]KeyPair, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT key_pair_id,account_id,name,fingerprint,public_key,created_at FROM key_pairs WHERE account_id=? ORDER BY name`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KeyPair{}
	for rows.Next() {
		var k KeyPair
		if err := rows.Scan(&k.KeyPairID, &k.AccountID, &k.Name, &k.Fingerprint, &k.PublicKey, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *ec2Repo) GetKeyPair(ctx context.Context, account, name string) (*KeyPair, error) {
	var k KeyPair
	err := r.d.QueryRowContext(ctx, `SELECT key_pair_id,account_id,name,fingerprint,public_key,created_at FROM key_pairs WHERE account_id=? AND name=?`, account, name).
		Scan(&k.KeyPairID, &k.AccountID, &k.Name, &k.Fingerprint, &k.PublicKey, &k.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &k, nil
}

func (r *ec2Repo) DeleteKeyPair(ctx context.Context, account, name string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM key_pairs WHERE account_id=? AND name=?`, account, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func nz(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

func (r *ec2Repo) SetBootResult(ctx context.Context, id, result string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE instances SET boot_result=? WHERE instance_id=?`, result, id)
	return err
}

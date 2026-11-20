package repositories

import (
	"context"
	"database/sql"

	"cloudsim/backend/internal/db"
)

type VPC struct {
	VPCID              string            `json:"vpc_id"`
	AccountID          string            `json:"-"`
	Name               string            `json:"name"`
	CIDRBlock          string            `json:"cidr_block"`
	IsDefault          bool              `json:"is_default"`
	EnableDNSSupport   bool              `json:"enable_dns_support"`
	EnableDNSHostnames bool              `json:"enable_dns_hostnames"`
	State              string            `json:"state"`
	Tags               map[string]string `json:"tags"`
	CreatedAt          string            `json:"created_at"`
}

type Subnet struct {
	SubnetID            string            `json:"subnet_id"`
	AccountID           string            `json:"-"`
	VPCID               string            `json:"vpc_id"`
	Name                string            `json:"name"`
	CIDRBlock           string            `json:"cidr_block"`
	AvailabilityZone    string            `json:"availability_zone"`
	MapPublicIPOnLaunch bool              `json:"map_public_ip_on_launch"`
	ExplicitRouteTable  string            `json:"-"`
	RouteTableID        string            `json:"route_table_id"`
	NetworkACLID        string            `json:"network_acl_id"`
	AvailableIPCount    int               `json:"available_ip_count"`
	IsPublic            bool              `json:"is_public"`
	Tags                map[string]string `json:"tags"`
	CreatedAt           string            `json:"created_at"`
}

type Route struct {
	DestinationCIDR string `json:"destination_cidr"`
	TargetID        string `json:"target_id"`
	State           string `json:"state"`
}

type RouteTable struct {
	RouteTableID string            `json:"route_table_id"`
	AccountID    string            `json:"-"`
	VPCID        string            `json:"vpc_id"`
	Name         string            `json:"name"`
	IsMain       bool              `json:"is_main"`
	Routes       []Route           `json:"routes"`
	SubnetIDs    []string          `json:"subnet_ids"`
	Tags         map[string]string `json:"tags"`
	CreatedAt    string            `json:"created_at"`
}

type InternetGateway struct {
	InternetGatewayID string            `json:"internet_gateway_id"`
	AccountID         string            `json:"-"`
	Name              string            `json:"name"`
	VPCID             *string           `json:"vpc_id"`
	State             string            `json:"state"`
	Tags              map[string]string `json:"tags"`
	CreatedAt         string            `json:"created_at"`
}

type NATGateway struct {
	NATGatewayID string            `json:"nat_gateway_id"`
	AccountID    string            `json:"-"`
	Name         string            `json:"name"`
	SubnetID     string            `json:"subnet_id"`
	VPCID        string            `json:"vpc_id"`
	State        string            `json:"state"`
	PublicIP     string            `json:"public_ip"`
	PrivateIP    string            `json:"private_ip"`
	Tags         map[string]string `json:"tags"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
}

type SGRule struct {
	RuleID                string `json:"rule_id"`
	GroupID               string `json:"-"`
	Direction             string `json:"-"`
	Protocol              string `json:"protocol"`
	FromPort              int    `json:"from_port"`
	ToPort                int    `json:"to_port"`
	CIDR                  string `json:"cidr"`
	SourceSecurityGroupID string `json:"source_security_group_id"`
	Description           string `json:"description"`
}

type SecurityGroup struct {
	GroupID     string            `json:"group_id"`
	AccountID   string            `json:"-"`
	VPCID       string            `json:"vpc_id"`
	GroupName   string            `json:"group_name"`
	Description string            `json:"description"`
	IsDefault   bool              `json:"is_default"`
	Ingress     []SGRule          `json:"ingress"`
	Egress      []SGRule          `json:"egress"`
	Tags        map[string]string `json:"tags"`
	CreatedAt   string            `json:"created_at"`
}

type ACLEntry struct {
	RuleNumber int    `json:"rule_number"`
	Egress     bool   `json:"egress"`
	Protocol   string `json:"protocol"`
	FromPort   int    `json:"from_port"`
	ToPort     int    `json:"to_port"`
	CIDR       string `json:"cidr"`
	Action     string `json:"action"`
}

type NetworkACL struct {
	NetworkACLID string            `json:"network_acl_id"`
	AccountID    string            `json:"-"`
	VPCID        string            `json:"vpc_id"`
	Name         string            `json:"name"`
	IsDefault    bool              `json:"is_default"`
	Entries      []ACLEntry        `json:"entries"`
	SubnetIDs    []string          `json:"subnet_ids"`
	Tags         map[string]string `json:"tags"`
	CreatedAt    string            `json:"created_at"`
}

type ENIAttachment struct {
	InstanceID  string `json:"instance_id"`
	DeviceIndex int    `json:"device_index"`
}

type NetworkInterface struct {
	NetworkInterfaceID string         `json:"network_interface_id"`
	AccountID          string         `json:"-"`
	SubnetID           string         `json:"subnet_id"`
	VPCID              string         `json:"vpc_id"`
	PrivateIP          string         `json:"private_ip"`
	PublicIP           string         `json:"public_ip"`
	Description        string         `json:"description"`
	SecurityGroupIDs   []string       `json:"security_group_ids"`
	Attachment         *ENIAttachment `json:"attachment"`
	Status             string         `json:"status"`
	CreatedAt          string         `json:"created_at"`
}

type NetworkRepo interface {
	InsertVPC(ctx context.Context, q Querier, v *VPC) error
	GetVPC(ctx context.Context, account, id string) (*VPC, error)
	ListVPCs(ctx context.Context, account string) ([]VPC, error)
	UpdateVPC(ctx context.Context, v *VPC) error
	DeleteVPC(ctx context.Context, q Querier, id string) error
	DefaultVPC(ctx context.Context, account string) (*VPC, error)

	InsertSubnet(ctx context.Context, q Querier, s *Subnet) error
	GetSubnet(ctx context.Context, account, id string) (*Subnet, error)
	ListSubnets(ctx context.Context, account, vpcID string) ([]Subnet, error)
	UpdateSubnet(ctx context.Context, s *Subnet) error
	DeleteSubnet(ctx context.Context, id string) error
	UsedIPs(ctx context.Context, subnetID string) ([]string, error)
	UsedIPsQ(ctx context.Context, q Querier, subnetID string) ([]string, error)

	InsertRouteTable(ctx context.Context, q Querier, rt *RouteTable) error
	GetRouteTable(ctx context.Context, account, id string) (*RouteTable, error)
	ListRouteTables(ctx context.Context, account, vpcID string) ([]RouteTable, error)
	DeleteRouteTable(ctx context.Context, id string) error
	PutRoute(ctx context.Context, q Querier, rtID string, r Route) error
	DeleteRoute(ctx context.Context, rtID, dest string) error
	AssociateRouteTable(ctx context.Context, subnetID, rtID string) error
	MainRouteTable(ctx context.Context, vpcID string) (*RouteTable, error)

	InsertIGW(ctx context.Context, q Querier, g *InternetGateway) error
	GetIGW(ctx context.Context, account, id string) (*InternetGateway, error)
	ListIGWs(ctx context.Context, account string) ([]InternetGateway, error)
	SetIGWVPC(ctx context.Context, q Querier, id string, vpcID *string) error
	DeleteIGW(ctx context.Context, id string) error

	InsertNAT(ctx context.Context, n *NATGateway) error
	GetNAT(ctx context.Context, account, id string) (*NATGateway, error)
	GetNATByID(ctx context.Context, id string) (*NATGateway, error)
	ListNATs(ctx context.Context, account string) ([]NATGateway, error)
	ListAllNATs(ctx context.Context) ([]NATGateway, error)
	SetNATState(ctx context.Context, id, state string) error
	DeleteNAT(ctx context.Context, id string) error

	InsertSG(ctx context.Context, q Querier, g *SecurityGroup) error
	GetSG(ctx context.Context, account, id string) (*SecurityGroup, error)
	ListSGs(ctx context.Context, account, vpcID string) ([]SecurityGroup, error)
	DeleteSG(ctx context.Context, id string) error
	InsertSGRule(ctx context.Context, q Querier, r *SGRule) error
	DeleteSGRule(ctx context.Context, groupID, ruleID string) error
	DefaultSG(ctx context.Context, vpcID string) (*SecurityGroup, error)
	SGInUse(ctx context.Context, groupID string) (int, error)

	InsertACL(ctx context.Context, q Querier, a *NetworkACL) error
	GetACL(ctx context.Context, account, id string) (*NetworkACL, error)
	ListACLs(ctx context.Context, account, vpcID string) ([]NetworkACL, error)
	DeleteACL(ctx context.Context, id string) error
	PutACLEntry(ctx context.Context, q Querier, aclID string, e ACLEntry) error
	DeleteACLEntry(ctx context.Context, aclID string, ruleNumber int, egress bool) error
	AssociateACL(ctx context.Context, subnetID, aclID string) error
	DefaultACL(ctx context.Context, vpcID string) (*NetworkACL, error)

	InsertENI(ctx context.Context, q Querier, e *NetworkInterface) error
	GetENI(ctx context.Context, account, id string) (*NetworkInterface, error)
	ListENIs(ctx context.Context, account string) ([]NetworkInterface, error)
	PrimaryENI(ctx context.Context, instanceID string) (*NetworkInterface, error)
	SetENISecurityGroups(ctx context.Context, q Querier, eniID string, groups []string) error
	DeleteENI(ctx context.Context, q Querier, id string) error
	DetachENIs(ctx context.Context, q Querier, instanceID string) error
}

type networkRepo struct{ d *db.DB }

func (r *networkRepo) q(q Querier) Querier {
	if q == nil {
		return r.d
	}
	return q
}

// ---- VPC

func (r *networkRepo) InsertVPC(ctx context.Context, q Querier, v *VPC) error {
	v.CreatedAt = db.Now()
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO vpcs(vpc_id,account_id,name,cidr_block,is_default,enable_dns_support,enable_dns_hostnames,state,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		v.VPCID, v.AccountID, v.Name, v.CIDRBlock, b2i(v.IsDefault), b2i(v.EnableDNSSupport), b2i(v.EnableDNSHostnames), v.State, v.CreatedAt)
	return err
}

const vpcCols = `vpc_id,account_id,name,cidr_block,is_default,enable_dns_support,enable_dns_hostnames,state,created_at`

func scanVPC(row interface{ Scan(...any) error }) (*VPC, error) {
	var v VPC
	var d, s, h int
	if err := row.Scan(&v.VPCID, &v.AccountID, &v.Name, &v.CIDRBlock, &d, &s, &h, &v.State, &v.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	v.IsDefault, v.EnableDNSSupport, v.EnableDNSHostnames = d == 1, s == 1, h == 1
	return &v, nil
}

func (r *networkRepo) GetVPC(ctx context.Context, account, id string) (*VPC, error) {
	return scanVPC(r.d.QueryRowContext(ctx, `SELECT `+vpcCols+` FROM vpcs WHERE vpc_id=? AND account_id=?`, id, account))
}

func (r *networkRepo) DefaultVPC(ctx context.Context, account string) (*VPC, error) {
	return scanVPC(r.d.QueryRowContext(ctx, `SELECT `+vpcCols+` FROM vpcs WHERE account_id=? AND is_default=1`, account))
}

func (r *networkRepo) ListVPCs(ctx context.Context, account string) ([]VPC, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+vpcCols+` FROM vpcs WHERE account_id=? ORDER BY created_at`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VPC{}
	for rows.Next() {
		v, err := scanVPC(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (r *networkRepo) UpdateVPC(ctx context.Context, v *VPC) error {
	_, err := r.d.ExecContext(ctx, `UPDATE vpcs SET name=?, enable_dns_support=?, enable_dns_hostnames=? WHERE vpc_id=?`,
		v.Name, b2i(v.EnableDNSSupport), b2i(v.EnableDNSHostnames), v.VPCID)
	return err
}

func (r *networkRepo) DeleteVPC(ctx context.Context, q Querier, id string) error {
	_, err := r.q(q).ExecContext(ctx, `DELETE FROM vpcs WHERE vpc_id=?`, id)
	return err
}

// ---- Subnets

func (r *networkRepo) InsertSubnet(ctx context.Context, q Querier, s *Subnet) error {
	s.CreatedAt = db.Now()
	var rt any
	if s.ExplicitRouteTable != "" {
		rt = s.ExplicitRouteTable
	}
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO subnets(subnet_id,account_id,vpc_id,name,cidr_block,availability_zone,map_public_ip_on_launch,route_table_id,network_acl_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		s.SubnetID, s.AccountID, s.VPCID, s.Name, s.CIDRBlock, s.AvailabilityZone, b2i(s.MapPublicIPOnLaunch), rt, s.NetworkACLID, s.CreatedAt)
	return err
}

// The effective route table is the explicit association or the VPC's main table.
const subnetSelect = `SELECT s.subnet_id,s.account_id,s.vpc_id,s.name,s.cidr_block,s.availability_zone,s.map_public_ip_on_launch,
 COALESCE(s.route_table_id,''), COALESCE(s.route_table_id,(SELECT route_table_id FROM route_tables m WHERE m.vpc_id=s.vpc_id AND m.is_main=1),''),
 s.network_acl_id,s.created_at,
 EXISTS(SELECT 1 FROM routes ro JOIN internet_gateways g ON g.internet_gateway_id=ro.target_id AND g.vpc_id=s.vpc_id
        WHERE ro.route_table_id=COALESCE(s.route_table_id,(SELECT route_table_id FROM route_tables m WHERE m.vpc_id=s.vpc_id AND m.is_main=1)) AND ro.destination_cidr='0.0.0.0/0')
 FROM subnets s`

func scanSubnet(row interface{ Scan(...any) error }) (*Subnet, error) {
	var s Subnet
	var mp, pub int
	if err := row.Scan(&s.SubnetID, &s.AccountID, &s.VPCID, &s.Name, &s.CIDRBlock, &s.AvailabilityZone, &mp, &s.ExplicitRouteTable, &s.RouteTableID, &s.NetworkACLID, &s.CreatedAt, &pub); err != nil {
		return nil, notFound(err)
	}
	s.MapPublicIPOnLaunch, s.IsPublic = mp == 1, pub == 1
	return &s, nil
}

func (r *networkRepo) GetSubnet(ctx context.Context, account, id string) (*Subnet, error) {
	return scanSubnet(r.d.QueryRowContext(ctx, subnetSelect+` WHERE s.subnet_id=? AND s.account_id=?`, id, account))
}

func (r *networkRepo) ListSubnets(ctx context.Context, account, vpcID string) ([]Subnet, error) {
	rows, err := r.d.QueryContext(ctx, subnetSelect+` WHERE s.account_id=? AND (?='' OR s.vpc_id=?) ORDER BY s.created_at`, account, vpcID, vpcID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subnet{}
	for rows.Next() {
		s, err := scanSubnet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *networkRepo) UpdateSubnet(ctx context.Context, s *Subnet) error {
	_, err := r.d.ExecContext(ctx, `UPDATE subnets SET name=?, map_public_ip_on_launch=? WHERE subnet_id=?`, s.Name, b2i(s.MapPublicIPOnLaunch), s.SubnetID)
	return err
}

func (r *networkRepo) DeleteSubnet(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM subnets WHERE subnet_id=?`, id)
	return err
}

func (r *networkRepo) UsedIPs(ctx context.Context, subnetID string) ([]string, error) {
	return r.UsedIPsQ(ctx, nil, subnetID)
}

// UsedIPsQ lists allocated addresses; call it inside the allocating transaction to avoid races.
func (r *networkRepo) UsedIPsQ(ctx context.Context, q Querier, subnetID string) ([]string, error) {
	rows, err := r.q(q).QueryContext(ctx, `SELECT private_ip FROM network_interfaces WHERE subnet_id=?1
		UNION SELECT private_ip FROM nat_gateways WHERE subnet_id=?1 AND state NOT IN ('deleted')`, subnetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, err
		}
		out = append(out, ip)
	}
	return out, rows.Err()
}

// ---- Route tables

func (r *networkRepo) InsertRouteTable(ctx context.Context, q Querier, rt *RouteTable) error {
	rt.CreatedAt = db.Now()
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO route_tables(route_table_id,account_id,vpc_id,name,is_main,created_at) VALUES(?,?,?,?,?,?)`,
		rt.RouteTableID, rt.AccountID, rt.VPCID, rt.Name, b2i(rt.IsMain), rt.CreatedAt)
	return err
}

func (r *networkRepo) loadRT(ctx context.Context, rt *RouteTable) error {
	// A route is a blackhole when its target no longer exists / is not attached.
	rows, err := r.d.QueryContext(ctx, `SELECT ro.destination_cidr, ro.target_id,
		CASE WHEN ro.target_id='local' THEN 'active'
		     WHEN ro.target_id LIKE 'igw-%' AND EXISTS(SELECT 1 FROM internet_gateways g WHERE g.internet_gateway_id=ro.target_id AND g.vpc_id=?) THEN 'active'
		     WHEN ro.target_id LIKE 'nat-%' AND EXISTS(SELECT 1 FROM nat_gateways n WHERE n.nat_gateway_id=ro.target_id AND n.state IN ('available','pending')) THEN 'active'
		     ELSE 'blackhole' END
		FROM routes ro WHERE ro.route_table_id=? ORDER BY ro.destination_cidr`, rt.VPCID, rt.RouteTableID)
	if err != nil {
		return err
	}
	rt.Routes = []Route{}
	for rows.Next() {
		var x Route
		if err := rows.Scan(&x.DestinationCIDR, &x.TargetID, &x.State); err != nil {
			rows.Close()
			return err
		}
		rt.Routes = append(rt.Routes, x)
	}
	rows.Close()
	q := `SELECT subnet_id FROM subnets WHERE route_table_id=?1 AND ?2 <> ''`
	if rt.IsMain {
		q = `SELECT subnet_id FROM subnets WHERE route_table_id=?1 OR (route_table_id IS NULL AND vpc_id=?2)`
	}
	srows, err := r.d.QueryContext(ctx, q, rt.RouteTableID, rt.VPCID)
	if err != nil {
		return err
	}
	defer srows.Close()
	rt.SubnetIDs = []string{}
	for srows.Next() {
		var id string
		if err := srows.Scan(&id); err != nil {
			return err
		}
		rt.SubnetIDs = append(rt.SubnetIDs, id)
	}
	return srows.Err()
}

func (r *networkRepo) rts(ctx context.Context, q string, args ...any) ([]RouteTable, error) {
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []RouteTable
	for rows.Next() {
		var rt RouteTable
		var m int
		if err := rows.Scan(&rt.RouteTableID, &rt.AccountID, &rt.VPCID, &rt.Name, &m, &rt.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		rt.IsMain = m == 1
		out = append(out, rt)
	}
	rows.Close()
	for i := range out {
		if err := r.loadRT(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []RouteTable{}
	}
	return out, nil
}

const rtCols = `SELECT route_table_id,account_id,vpc_id,name,is_main,created_at FROM route_tables`

func (r *networkRepo) GetRouteTable(ctx context.Context, account, id string) (*RouteTable, error) {
	l, err := r.rts(ctx, rtCols+` WHERE route_table_id=? AND account_id=?`, id, account)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (r *networkRepo) MainRouteTable(ctx context.Context, vpcID string) (*RouteTable, error) {
	l, err := r.rts(ctx, rtCols+` WHERE vpc_id=? AND is_main=1`, vpcID)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (r *networkRepo) ListRouteTables(ctx context.Context, account, vpcID string) ([]RouteTable, error) {
	return r.rts(ctx, rtCols+` WHERE account_id=? AND (?='' OR vpc_id=?) ORDER BY created_at`, account, vpcID, vpcID)
}

func (r *networkRepo) DeleteRouteTable(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM route_tables WHERE route_table_id=?`, id)
	return err
}

func (r *networkRepo) PutRoute(ctx context.Context, q Querier, rtID string, x Route) error {
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO routes(route_table_id,destination_cidr,target_id) VALUES(?,?,?)
		ON CONFLICT(route_table_id,destination_cidr) DO UPDATE SET target_id=excluded.target_id`, rtID, x.DestinationCIDR, x.TargetID)
	return err
}

func (r *networkRepo) DeleteRoute(ctx context.Context, rtID, dest string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM routes WHERE route_table_id=? AND destination_cidr=?`, rtID, dest)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *networkRepo) AssociateRouteTable(ctx context.Context, subnetID, rtID string) error {
	var v any
	if rtID != "" {
		v = rtID
	}
	_, err := r.d.ExecContext(ctx, `UPDATE subnets SET route_table_id=? WHERE subnet_id=?`, v, subnetID)
	return err
}

// ---- Internet gateways

func (r *networkRepo) InsertIGW(ctx context.Context, q Querier, g *InternetGateway) error {
	g.CreatedAt = db.Now()
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO internet_gateways(internet_gateway_id,account_id,name,vpc_id,created_at) VALUES(?,?,?,?,?)`,
		g.InternetGatewayID, g.AccountID, g.Name, g.VPCID, g.CreatedAt)
	return err
}

func scanIGW(row interface{ Scan(...any) error }) (*InternetGateway, error) {
	var g InternetGateway
	var vpc sql.NullString
	if err := row.Scan(&g.InternetGatewayID, &g.AccountID, &g.Name, &vpc, &g.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	g.State = "detached"
	if vpc.Valid {
		g.VPCID = &vpc.String
		g.State = "attached"
	}
	return &g, nil
}

func (r *networkRepo) GetIGW(ctx context.Context, account, id string) (*InternetGateway, error) {
	return scanIGW(r.d.QueryRowContext(ctx, `SELECT internet_gateway_id,account_id,name,vpc_id,created_at FROM internet_gateways WHERE internet_gateway_id=? AND account_id=?`, id, account))
}

func (r *networkRepo) ListIGWs(ctx context.Context, account string) ([]InternetGateway, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT internet_gateway_id,account_id,name,vpc_id,created_at FROM internet_gateways WHERE account_id=? ORDER BY created_at`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InternetGateway{}
	for rows.Next() {
		g, err := scanIGW(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (r *networkRepo) SetIGWVPC(ctx context.Context, q Querier, id string, vpcID *string) error {
	_, err := r.q(q).ExecContext(ctx, `UPDATE internet_gateways SET vpc_id=? WHERE internet_gateway_id=?`, vpcID, id)
	return err
}

func (r *networkRepo) DeleteIGW(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM internet_gateways WHERE internet_gateway_id=?`, id)
	return err
}

// ---- NAT gateways

const natCols = `nat_gateway_id,account_id,name,subnet_id,vpc_id,state,public_ip,private_ip,created_at,updated_at`

func scanNAT(row interface{ Scan(...any) error }) (*NATGateway, error) {
	var n NATGateway
	if err := row.Scan(&n.NATGatewayID, &n.AccountID, &n.Name, &n.SubnetID, &n.VPCID, &n.State, &n.PublicIP, &n.PrivateIP, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	return &n, nil
}

func (r *networkRepo) InsertNAT(ctx context.Context, n *NATGateway) error {
	n.CreatedAt = db.Now()
	n.UpdatedAt = n.CreatedAt
	_, err := r.d.ExecContext(ctx, `INSERT INTO nat_gateways(`+natCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		n.NATGatewayID, n.AccountID, n.Name, n.SubnetID, n.VPCID, n.State, n.PublicIP, n.PrivateIP, n.CreatedAt, n.UpdatedAt)
	return err
}

func (r *networkRepo) GetNAT(ctx context.Context, account, id string) (*NATGateway, error) {
	return scanNAT(r.d.QueryRowContext(ctx, `SELECT `+natCols+` FROM nat_gateways WHERE nat_gateway_id=? AND account_id=?`, id, account))
}

func (r *networkRepo) GetNATByID(ctx context.Context, id string) (*NATGateway, error) {
	return scanNAT(r.d.QueryRowContext(ctx, `SELECT `+natCols+` FROM nat_gateways WHERE nat_gateway_id=?`, id))
}

func (r *networkRepo) nats(ctx context.Context, q string, args ...any) ([]NATGateway, error) {
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NATGateway{}
	for rows.Next() {
		n, err := scanNAT(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (r *networkRepo) ListNATs(ctx context.Context, account string) ([]NATGateway, error) {
	return r.nats(ctx, `SELECT `+natCols+` FROM nat_gateways WHERE account_id=? ORDER BY created_at`, account)
}

func (r *networkRepo) ListAllNATs(ctx context.Context) ([]NATGateway, error) {
	return r.nats(ctx, `SELECT `+natCols+` FROM nat_gateways WHERE state <> 'deleted'`)
}

func (r *networkRepo) SetNATState(ctx context.Context, id, state string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE nat_gateways SET state=?, updated_at=? WHERE nat_gateway_id=?`, state, db.Now(), id)
	return err
}

func (r *networkRepo) DeleteNAT(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM nat_gateways WHERE nat_gateway_id=?`, id)
	return err
}

// ---- Security groups

func (r *networkRepo) InsertSG(ctx context.Context, q Querier, g *SecurityGroup) error {
	g.CreatedAt = db.Now()
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO security_groups(group_id,account_id,vpc_id,group_name,description,is_default,created_at) VALUES(?,?,?,?,?,?,?)`,
		g.GroupID, g.AccountID, g.VPCID, g.GroupName, g.Description, b2i(g.IsDefault), g.CreatedAt)
	return err
}

func (r *networkRepo) sgs(ctx context.Context, q string, args ...any) ([]SecurityGroup, error) {
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []SecurityGroup
	idx := map[string]int{}
	for rows.Next() {
		var g SecurityGroup
		var d int
		if err := rows.Scan(&g.GroupID, &g.AccountID, &g.VPCID, &g.GroupName, &g.Description, &d, &g.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		g.IsDefault = d == 1
		g.Ingress, g.Egress = []SGRule{}, []SGRule{}
		idx[g.GroupID] = len(out)
		out = append(out, g)
	}
	rows.Close()
	if len(out) == 0 {
		return []SecurityGroup{}, nil
	}
	args2 := make([]any, 0, len(out))
	ph := ""
	for i, g := range out {
		if i > 0 {
			ph += ","
		}
		ph += "?"
		args2 = append(args2, g.GroupID)
	}
	rrows, err := r.d.QueryContext(ctx, `SELECT rule_id,group_id,direction,protocol,from_port,to_port,cidr,source_security_group_id,description FROM security_group_rules WHERE group_id IN (`+ph+`) ORDER BY rowid`, args2...)
	if err != nil {
		return nil, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var x SGRule
		if err := rrows.Scan(&x.RuleID, &x.GroupID, &x.Direction, &x.Protocol, &x.FromPort, &x.ToPort, &x.CIDR, &x.SourceSecurityGroupID, &x.Description); err != nil {
			return nil, err
		}
		g := &out[idx[x.GroupID]]
		if x.Direction == "ingress" {
			g.Ingress = append(g.Ingress, x)
		} else {
			g.Egress = append(g.Egress, x)
		}
	}
	return out, rrows.Err()
}

const sgCols = `SELECT group_id,account_id,vpc_id,group_name,description,is_default,created_at FROM security_groups`

func (r *networkRepo) GetSG(ctx context.Context, account, id string) (*SecurityGroup, error) {
	l, err := r.sgs(ctx, sgCols+` WHERE group_id=? AND account_id=?`, id, account)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (r *networkRepo) DefaultSG(ctx context.Context, vpcID string) (*SecurityGroup, error) {
	l, err := r.sgs(ctx, sgCols+` WHERE vpc_id=? AND is_default=1`, vpcID)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (r *networkRepo) ListSGs(ctx context.Context, account, vpcID string) ([]SecurityGroup, error) {
	return r.sgs(ctx, sgCols+` WHERE account_id=? AND (?='' OR vpc_id=?) ORDER BY created_at`, account, vpcID, vpcID)
}

func (r *networkRepo) DeleteSG(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM security_groups WHERE group_id=?`, id)
	return err
}

func (r *networkRepo) InsertSGRule(ctx context.Context, q Querier, x *SGRule) error {
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO security_group_rules(rule_id,group_id,direction,protocol,from_port,to_port,cidr,source_security_group_id,description) VALUES(?,?,?,?,?,?,?,?,?)`,
		x.RuleID, x.GroupID, x.Direction, x.Protocol, x.FromPort, x.ToPort, x.CIDR, x.SourceSecurityGroupID, x.Description)
	return err
}

func (r *networkRepo) DeleteSGRule(ctx context.Context, groupID, ruleID string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM security_group_rules WHERE group_id=? AND rule_id=?`, groupID, ruleID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *networkRepo) SGInUse(ctx context.Context, groupID string) (int, error) {
	var n int
	err := r.d.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM eni_security_groups WHERE group_id=?1) +
		(SELECT COUNT(*) FROM security_group_rules WHERE source_security_group_id=?1 AND group_id<>?1)`, groupID).Scan(&n)
	return n, err
}

// ---- Network ACLs

func (r *networkRepo) InsertACL(ctx context.Context, q Querier, a *NetworkACL) error {
	a.CreatedAt = db.Now()
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO network_acls(network_acl_id,account_id,vpc_id,name,is_default,created_at) VALUES(?,?,?,?,?,?)`,
		a.NetworkACLID, a.AccountID, a.VPCID, a.Name, b2i(a.IsDefault), a.CreatedAt)
	return err
}

func (r *networkRepo) acls(ctx context.Context, q string, args ...any) ([]NetworkACL, error) {
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []NetworkACL
	for rows.Next() {
		var a NetworkACL
		var d int
		if err := rows.Scan(&a.NetworkACLID, &a.AccountID, &a.VPCID, &a.Name, &d, &a.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		a.IsDefault = d == 1
		out = append(out, a)
	}
	rows.Close()
	for i := range out {
		a := &out[i]
		a.Entries, a.SubnetIDs = []ACLEntry{}, []string{}
		er, err := r.d.QueryContext(ctx, `SELECT rule_number,egress,protocol,from_port,to_port,cidr,action FROM network_acl_entries WHERE network_acl_id=? ORDER BY egress, rule_number`, a.NetworkACLID)
		if err != nil {
			return nil, err
		}
		for er.Next() {
			var e ACLEntry
			var eg int
			if err := er.Scan(&e.RuleNumber, &eg, &e.Protocol, &e.FromPort, &e.ToPort, &e.CIDR, &e.Action); err != nil {
				er.Close()
				return nil, err
			}
			e.Egress = eg == 1
			a.Entries = append(a.Entries, e)
		}
		er.Close()
		sr, err := r.d.QueryContext(ctx, `SELECT subnet_id FROM subnets WHERE network_acl_id=?`, a.NetworkACLID)
		if err != nil {
			return nil, err
		}
		for sr.Next() {
			var s string
			if err := sr.Scan(&s); err != nil {
				sr.Close()
				return nil, err
			}
			a.SubnetIDs = append(a.SubnetIDs, s)
		}
		sr.Close()
	}
	if out == nil {
		out = []NetworkACL{}
	}
	return out, nil
}

const aclCols = `SELECT network_acl_id,account_id,vpc_id,name,is_default,created_at FROM network_acls`

func (r *networkRepo) GetACL(ctx context.Context, account, id string) (*NetworkACL, error) {
	l, err := r.acls(ctx, aclCols+` WHERE network_acl_id=? AND account_id=?`, id, account)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (r *networkRepo) DefaultACL(ctx context.Context, vpcID string) (*NetworkACL, error) {
	l, err := r.acls(ctx, aclCols+` WHERE vpc_id=? AND is_default=1`, vpcID)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (r *networkRepo) ListACLs(ctx context.Context, account, vpcID string) ([]NetworkACL, error) {
	return r.acls(ctx, aclCols+` WHERE account_id=? AND (?='' OR vpc_id=?) ORDER BY created_at`, account, vpcID, vpcID)
}

func (r *networkRepo) DeleteACL(ctx context.Context, id string) error {
	_, err := r.d.ExecContext(ctx, `DELETE FROM network_acls WHERE network_acl_id=?`, id)
	return err
}

func (r *networkRepo) PutACLEntry(ctx context.Context, q Querier, aclID string, e ACLEntry) error {
	_, err := r.q(q).ExecContext(ctx, `INSERT INTO network_acl_entries(network_acl_id,rule_number,egress,protocol,from_port,to_port,cidr,action) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(network_acl_id,egress,rule_number) DO UPDATE SET protocol=excluded.protocol, from_port=excluded.from_port, to_port=excluded.to_port, cidr=excluded.cidr, action=excluded.action`,
		aclID, e.RuleNumber, b2i(e.Egress), e.Protocol, e.FromPort, e.ToPort, e.CIDR, e.Action)
	return err
}

func (r *networkRepo) DeleteACLEntry(ctx context.Context, aclID string, n int, egress bool) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM network_acl_entries WHERE network_acl_id=? AND rule_number=? AND egress=?`, aclID, n, b2i(egress))
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *networkRepo) AssociateACL(ctx context.Context, subnetID, aclID string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE subnets SET network_acl_id=? WHERE subnet_id=?`, aclID, subnetID)
	return err
}

// ---- ENIs

func (r *networkRepo) InsertENI(ctx context.Context, q Querier, e *NetworkInterface) error {
	e.CreatedAt = db.Now()
	var inst any
	if e.Attachment != nil {
		inst = e.Attachment.InstanceID
	}
	qq := r.q(q)
	_, err := qq.ExecContext(ctx, `INSERT INTO network_interfaces(network_interface_id,account_id,subnet_id,vpc_id,private_ip,public_ip,description,instance_id,device_index,created_at) VALUES(?,?,?,?,?,?,?,?,0,?)`,
		e.NetworkInterfaceID, e.AccountID, e.SubnetID, e.VPCID, e.PrivateIP, e.PublicIP, e.Description, inst, e.CreatedAt)
	if err != nil {
		return err
	}
	return r.SetENISecurityGroups(ctx, qq, e.NetworkInterfaceID, e.SecurityGroupIDs)
}

func (r *networkRepo) SetENISecurityGroups(ctx context.Context, q Querier, eniID string, groups []string) error {
	qq := r.q(q)
	if _, err := qq.ExecContext(ctx, `DELETE FROM eni_security_groups WHERE network_interface_id=?`, eniID); err != nil {
		return err
	}
	for _, g := range groups {
		if _, err := qq.ExecContext(ctx, `INSERT INTO eni_security_groups(network_interface_id,group_id) VALUES(?,?)`, eniID, g); err != nil {
			return err
		}
	}
	return nil
}

const eniCols = `SELECT network_interface_id,account_id,subnet_id,vpc_id,private_ip,public_ip,description,instance_id,device_index,created_at,
 COALESCE((SELECT group_concat(group_id) FROM eni_security_groups s WHERE s.network_interface_id=network_interfaces.network_interface_id),'') FROM network_interfaces`

func (r *networkRepo) enis(ctx context.Context, q string, args ...any) ([]NetworkInterface, error) {
	rows, err := r.d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkInterface{}
	for rows.Next() {
		var e NetworkInterface
		var inst sql.NullString
		var dev int
		var groups string
		if err := rows.Scan(&e.NetworkInterfaceID, &e.AccountID, &e.SubnetID, &e.VPCID, &e.PrivateIP, &e.PublicIP, &e.Description, &inst, &dev, &e.CreatedAt, &groups); err != nil {
			return nil, err
		}
		e.SecurityGroupIDs = splitCSV(groups)
		e.Status = "available"
		if inst.Valid {
			e.Attachment = &ENIAttachment{InstanceID: inst.String, DeviceIndex: dev}
			e.Status = "in-use"
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func splitCSV(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func (r *networkRepo) GetENI(ctx context.Context, account, id string) (*NetworkInterface, error) {
	l, err := r.enis(ctx, eniCols+` WHERE network_interface_id=? AND account_id=?`, id, account)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (r *networkRepo) ListENIs(ctx context.Context, account string) ([]NetworkInterface, error) {
	return r.enis(ctx, eniCols+` WHERE account_id=? ORDER BY created_at`, account)
}

func (r *networkRepo) PrimaryENI(ctx context.Context, instanceID string) (*NetworkInterface, error) {
	l, err := r.enis(ctx, eniCols+` WHERE instance_id=? AND device_index=0`, instanceID)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (r *networkRepo) DeleteENI(ctx context.Context, q Querier, id string) error {
	_, err := r.q(q).ExecContext(ctx, `DELETE FROM network_interfaces WHERE network_interface_id=?`, id)
	return err
}

func (r *networkRepo) DetachENIs(ctx context.Context, q Querier, instanceID string) error {
	_, err := r.q(q).ExecContext(ctx, `DELETE FROM network_interfaces WHERE instance_id=?`, instanceID)
	return err
}

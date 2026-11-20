package services

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strings"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

// NetworkService implements VPC networking.
type NetworkService struct{ *Deps }

func NewNetworkService(d *Deps) *NetworkService { return &NetworkService{d} }

func (s *NetworkService) repo() repositories.NetworkRepo { return s.Store.Network }

type CreateVPCInput struct {
	CIDRBlock string            `json:"cidr_block"`
	Name      string            `json:"name"`
	Tags      map[string]string `json:"tags"`
}

// CreateVPC creates a VPC with its main route table, default network ACL and default security group.
func (s *NetworkService) CreateVPC(ctx context.Context, account string, in CreateVPCInput, isDefault bool) (*repositories.VPC, error) {
	p, err := ParseCIDR(in.CIDRBlock)
	if err != nil {
		return nil, err
	}
	if p.Bits() < 16 || p.Bits() > 28 {
		return nil, apierr.Validation("VPC CIDR prefix must be between /16 and /28")
	}
	if len(in.Name) > 255 {
		return nil, apierr.Validation("name too long")
	}
	if err := ValidateTags(in.Tags); err != nil {
		return nil, err
	}
	v := &repositories.VPC{VPCID: ids.New("vpc"), AccountID: account, Name: in.Name, CIDRBlock: p.String(), IsDefault: isDefault,
		EnableDNSSupport: true, EnableDNSHostnames: true, State: "available"}
	if _, err := s.Engine.Register(ctx, v.VPCID, simulator.RegisterRequest{Kind: "vpc", Owner: account, Attributes: map[string]any{"cidr": v.CIDRBlock}}); err != nil {
		return nil, EngineErr(err)
	}
	rt := &repositories.RouteTable{RouteTableID: ids.New("rtb"), AccountID: account, VPCID: v.VPCID, Name: "main", IsMain: true}
	acl := &repositories.NetworkACL{NetworkACLID: ids.New("acl"), AccountID: account, VPCID: v.VPCID, Name: "default", IsDefault: true}
	sg := &repositories.SecurityGroup{GroupID: ids.New("sg"), AccountID: account, VPCID: v.VPCID, GroupName: "default", Description: "default VPC security group", IsDefault: true}
	err = s.Store.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.repo().InsertVPC(ctx, tx, v); err != nil {
			return err
		}
		if err := s.repo().InsertRouteTable(ctx, tx, rt); err != nil {
			return err
		}
		if err := s.repo().PutRoute(ctx, tx, rt.RouteTableID, repositories.Route{DestinationCIDR: v.CIDRBlock, TargetID: "local"}); err != nil {
			return err
		}
		if err := s.repo().InsertACL(ctx, tx, acl); err != nil {
			return err
		}
		for _, eg := range []bool{false, true} {
			if err := s.repo().PutACLEntry(ctx, tx, acl.NetworkACLID, repositories.ACLEntry{RuleNumber: 100, Egress: eg, Protocol: "-1", FromPort: 0, ToPort: 65535, CIDR: "0.0.0.0/0", Action: "allow"}); err != nil {
				return err
			}
		}
		if err := s.repo().InsertSG(ctx, tx, sg); err != nil {
			return err
		}
		if err := s.repo().InsertSGRule(ctx, tx, &repositories.SGRule{RuleID: ids.New("sgr"), GroupID: sg.GroupID, Direction: "egress", Protocol: "-1", FromPort: 0, ToPort: 65535, CIDR: "0.0.0.0/0", Description: "allow all outbound"}); err != nil {
			return err
		}
		if err := s.repo().InsertSGRule(ctx, tx, &repositories.SGRule{RuleID: ids.New("sgr"), GroupID: sg.GroupID, Direction: "ingress", Protocol: "-1", FromPort: 0, ToPort: 65535, SourceSecurityGroupID: sg.GroupID, Description: "allow traffic from members of this group"}); err != nil {
			return err
		}
		return s.Store.Tags.Replace(ctx, tx, account, v.VPCID, withName(in.Tags, in.Name))
	})
	if err != nil {
		_ = s.Engine.Deregister(ctx, v.VPCID)
		return nil, err
	}
	for _, id := range []string{rt.RouteTableID, acl.NetworkACLID, sg.GroupID} {
		kind := map[string]string{"rtb": "route_table", "acl": "network_acl", "sg": "security_group"}[strings.SplitN(id, "-", 2)[0]]
		_, _ = s.Engine.Register(ctx, id, simulator.RegisterRequest{Kind: kind, Owner: account, DependsOn: []string{v.VPCID}})
	}
	s.RecordEvent(ctx, account, v.VPCID, "vpc", "created", "VPC "+v.VPCID+" created ("+v.CIDRBlock+")", nil)
	v.Tags = withName(in.Tags, in.Name)
	return v, nil
}

func withName(tags map[string]string, name string) map[string]string {
	out := map[string]string{}
	for k, v := range tags {
		out[k] = v
	}
	if name != "" {
		out["Name"] = name
	}
	return out
}

// EnsureDefaultVPC returns the account's default VPC, creating 172.31.0.0/16 with a public subnet,
// attached internet gateway and default route on first use.
func (s *NetworkService) EnsureDefaultVPC(ctx context.Context, account string) (*repositories.VPC, *repositories.Subnet, error) {
	v, err := s.repo().DefaultVPC(ctx, account)
	if errors.Is(err, repositories.ErrNotFound) {
		v, err = s.CreateVPC(ctx, account, CreateVPCInput{CIDRBlock: "172.31.0.0/16", Name: "default"}, true)
		if err != nil {
			return nil, nil, err
		}
		igw, err := s.CreateIGW(ctx, account, "default-igw", nil)
		if err != nil {
			return nil, nil, err
		}
		if err := s.AttachIGW(ctx, account, igw.InternetGatewayID, v.VPCID); err != nil {
			return nil, nil, err
		}
		main, err := s.repo().MainRouteTable(ctx, v.VPCID)
		if err != nil {
			return nil, nil, err
		}
		if err := s.AddRoute(ctx, account, main.RouteTableID, "0.0.0.0/0", igw.InternetGatewayID); err != nil {
			return nil, nil, err
		}
		if _, err := s.CreateSubnet(ctx, account, CreateSubnetInput{VPCID: v.VPCID, CIDRBlock: "172.31.0.0/20", Name: "default-a", MapPublicIP: true}); err != nil {
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	}
	subs, err := s.repo().ListSubnets(ctx, account, v.VPCID)
	if err != nil {
		return nil, nil, err
	}
	if len(subs) == 0 {
		return nil, nil, apierr.Validation("default VPC has no subnets; specify subnet_id")
	}
	return v, &subs[0], nil
}

func (s *NetworkService) GetVPC(ctx context.Context, account, id string) (*repositories.VPC, error) {
	v, err := s.repo().GetVPC(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "VPC "+id)
	}
	v.Tags, _ = s.Store.Tags.Get(ctx, id)
	return v, nil
}

func (s *NetworkService) ListVPCs(ctx context.Context, account string) ([]repositories.VPC, error) {
	l, err := s.repo().ListVPCs(ctx, account)
	if err != nil {
		return nil, err
	}
	return l, s.attachTags(ctx, len(l), func(i int) (string, *map[string]string) { return l[i].VPCID, &l[i].Tags })
}

func (s *NetworkService) attachTags(ctx context.Context, n int, at func(int) (string, *map[string]string)) error {
	idsList := make([]string, n)
	for i := 0; i < n; i++ {
		idsList[i], _ = at(i)
	}
	m, err := s.Store.Tags.GetMany(ctx, idsList)
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		id, dst := at(i)
		if m[id] != nil {
			*dst = m[id]
		} else {
			*dst = map[string]string{}
		}
	}
	return nil
}

type UpdateVPCInput struct {
	Name               *string `json:"name"`
	EnableDNSSupport   *bool   `json:"enable_dns_support"`
	EnableDNSHostnames *bool   `json:"enable_dns_hostnames"`
}

func (s *NetworkService) UpdateVPC(ctx context.Context, account, id string, in UpdateVPCInput) (*repositories.VPC, error) {
	v, err := s.GetVPC(ctx, account, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		v.Name = *in.Name
		v.Tags = withName(v.Tags, v.Name)
		_ = s.Store.Tags.Replace(ctx, nil, account, id, v.Tags)
	}
	if in.EnableDNSSupport != nil {
		v.EnableDNSSupport = *in.EnableDNSSupport
	}
	if in.EnableDNSHostnames != nil {
		v.EnableDNSHostnames = *in.EnableDNSHostnames
	}
	return v, s.repo().UpdateVPC(ctx, v)
}

// DeleteVPC removes a VPC once it has no subnets, gateways or instances. The main route table,
// default ACL and default SG are removed with it.
func (s *NetworkService) DeleteVPC(ctx context.Context, account, id string) error {
	v, err := s.GetVPC(ctx, account, id)
	if err != nil {
		return err
	}
	subs, err := s.repo().ListSubnets(ctx, account, id)
	if err != nil {
		return err
	}
	var deps []string
	for _, sn := range subs {
		deps = append(deps, sn.SubnetID)
	}
	igws, _ := s.repo().ListIGWs(ctx, account)
	for _, g := range igws {
		if g.VPCID != nil && *g.VPCID == id {
			deps = append(deps, g.InternetGatewayID)
		}
	}
	if len(deps) > 0 {
		return apierr.Dependency("VPC %s has dependencies and cannot be deleted: %s", id, strings.Join(deps, ", "))
	}
	sgs, _ := s.repo().ListSGs(ctx, account, id)
	for _, g := range sgs {
		if !g.IsDefault {
			return apierr.Dependency("VPC %s still has security group %s", id, g.GroupID)
		}
	}
	rts, _ := s.repo().ListRouteTables(ctx, account, id)
	for _, rt := range rts {
		if !rt.IsMain {
			return apierr.Dependency("VPC %s still has route table %s", id, rt.RouteTableID)
		}
	}
	acls, _ := s.repo().ListACLs(ctx, account, id)
	for _, a := range acls {
		if !a.IsDefault {
			return apierr.Dependency("VPC %s still has network ACL %s", id, a.NetworkACLID)
		}
	}
	// deregister children before the parent so the engine dependency check passes
	for _, g := range sgs {
		_ = s.Engine.Deregister(ctx, g.GroupID)
	}
	for _, rt := range rts {
		_ = s.Engine.Deregister(ctx, rt.RouteTableID)
	}
	for _, a := range acls {
		_ = s.Engine.Deregister(ctx, a.NetworkACLID)
	}
	if err := s.Engine.Deregister(ctx, id); err != nil {
		return EngineErr(err)
	}
	err = s.Store.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.Tags.Delete(ctx, tx, id); err != nil {
			return err
		}
		return s.repo().DeleteVPC(ctx, tx, id)
	})
	if err != nil {
		return RepoErr(err, "VPC")
	}
	s.RecordEvent(ctx, account, id, "vpc", "deleted", "VPC "+id+" deleted", map[string]any{"cidr": v.CIDRBlock})
	return nil
}

// ---- Subnets

type CreateSubnetInput struct {
	VPCID            string            `json:"vpc_id"`
	CIDRBlock        string            `json:"cidr_block"`
	Name             string            `json:"name"`
	AvailabilityZone string            `json:"availability_zone"`
	MapPublicIP      bool              `json:"map_public_ip_on_launch"`
	Tags             map[string]string `json:"tags"`
}

var validAZ = map[string]bool{"local-1a": true, "local-1b": true, "local-1c": true}

func (s *NetworkService) CreateSubnet(ctx context.Context, account string, in CreateSubnetInput) (*repositories.Subnet, error) {
	v, err := s.GetVPC(ctx, account, in.VPCID)
	if err != nil {
		return nil, err
	}
	p, err := ParseCIDR(in.CIDRBlock)
	if err != nil {
		return nil, err
	}
	vp := netip.MustParsePrefix(v.CIDRBlock)
	if !Contains(vp, p) {
		return nil, apierr.Validation("subnet CIDR %s is not within VPC CIDR %s", p, vp)
	}
	if p.Bits() > 28 {
		return nil, apierr.Validation("subnet prefix must be /28 or larger")
	}
	siblings, err := s.repo().ListSubnets(ctx, account, in.VPCID)
	if err != nil {
		return nil, err
	}
	for _, sb := range siblings {
		if Overlaps(netip.MustParsePrefix(sb.CIDRBlock), p) {
			return nil, apierr.Conflict("subnet CIDR %s overlaps existing subnet %s (%s)", p, sb.SubnetID, sb.CIDRBlock)
		}
	}
	if in.AvailabilityZone == "" {
		in.AvailabilityZone = "local-1a"
	}
	if !validAZ[in.AvailabilityZone] {
		return nil, apierr.Validation("availability zone must be one of local-1a, local-1b, local-1c")
	}
	if err := ValidateTags(in.Tags); err != nil {
		return nil, err
	}
	acl, err := s.repo().DefaultACL(ctx, v.VPCID)
	if err != nil {
		return nil, err
	}
	sn := &repositories.Subnet{SubnetID: ids.New("subnet"), AccountID: account, VPCID: v.VPCID, Name: in.Name, CIDRBlock: p.String(),
		AvailabilityZone: in.AvailabilityZone, MapPublicIPOnLaunch: in.MapPublicIP, NetworkACLID: acl.NetworkACLID}
	if _, err := s.Engine.Register(ctx, sn.SubnetID, simulator.RegisterRequest{Kind: "subnet", Owner: account, DependsOn: []string{v.VPCID}, Attributes: map[string]any{"cidr": sn.CIDRBlock}}); err != nil {
		return nil, EngineErr(err)
	}
	err = s.Store.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.repo().InsertSubnet(ctx, tx, sn); err != nil {
			return err
		}
		return s.Store.Tags.Replace(ctx, tx, account, sn.SubnetID, withName(in.Tags, in.Name))
	})
	if err != nil {
		_ = s.Engine.Deregister(ctx, sn.SubnetID)
		return nil, err
	}
	s.RecordEvent(ctx, account, sn.SubnetID, "subnet", "created", "Subnet "+sn.SubnetID+" created ("+sn.CIDRBlock+") in "+v.VPCID, nil)
	return s.GetSubnet(ctx, account, sn.SubnetID)
}

func (s *NetworkService) decorateSubnet(ctx context.Context, sn *repositories.Subnet) {
	used, _ := s.repo().UsedIPs(ctx, sn.SubnetID)
	sn.AvailableIPCount = UsableIPs(netip.MustParsePrefix(sn.CIDRBlock)) - len(used)
}

func (s *NetworkService) GetSubnet(ctx context.Context, account, id string) (*repositories.Subnet, error) {
	sn, err := s.repo().GetSubnet(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "subnet "+id)
	}
	s.decorateSubnet(ctx, sn)
	sn.Tags, _ = s.Store.Tags.Get(ctx, id)
	return sn, nil
}

func (s *NetworkService) ListSubnets(ctx context.Context, account, vpcID string) ([]repositories.Subnet, error) {
	l, err := s.repo().ListSubnets(ctx, account, vpcID)
	if err != nil {
		return nil, err
	}
	for i := range l {
		s.decorateSubnet(ctx, &l[i])
	}
	return l, s.attachTags(ctx, len(l), func(i int) (string, *map[string]string) { return l[i].SubnetID, &l[i].Tags })
}

type UpdateSubnetInput struct {
	Name        *string `json:"name"`
	MapPublicIP *bool   `json:"map_public_ip_on_launch"`
}

func (s *NetworkService) UpdateSubnet(ctx context.Context, account, id string, in UpdateSubnetInput) (*repositories.Subnet, error) {
	sn, err := s.GetSubnet(ctx, account, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		sn.Name = *in.Name
		_ = s.Store.Tags.Replace(ctx, nil, account, id, withName(sn.Tags, sn.Name))
	}
	if in.MapPublicIP != nil {
		sn.MapPublicIPOnLaunch = *in.MapPublicIP
	}
	if err := s.repo().UpdateSubnet(ctx, sn); err != nil {
		return nil, err
	}
	return s.GetSubnet(ctx, account, id)
}

func (s *NetworkService) DeleteSubnet(ctx context.Context, account, id string) error {
	if _, err := s.GetSubnet(ctx, account, id); err != nil {
		return err
	}
	n, err := s.Store.EC2.CountActiveInSubnet(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return apierr.Dependency("subnet %s has %d instance(s) that are not terminated", id, n)
	}
	nats, _ := s.repo().ListNATs(ctx, account)
	for _, g := range nats {
		if g.SubnetID == id && g.State != "deleted" {
			return apierr.Dependency("subnet %s contains NAT gateway %s", id, g.NATGatewayID)
		}
	}
	if err := s.Engine.Deregister(ctx, id); err != nil {
		return EngineErr(err)
	}
	// Remove ENIs of terminated instances still referencing this subnet, then the subnet.
	if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM network_interfaces WHERE subnet_id=? AND (instance_id IS NULL OR instance_id IN (SELECT instance_id FROM instances WHERE state='terminated'))`, id); err != nil {
		return err
	}
	if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM instances WHERE subnet_id=? AND state='terminated'`, id); err != nil {
		return err
	}
	if err := s.repo().DeleteSubnet(ctx, id); err != nil {
		return RepoErr(err, "subnet")
	}
	_ = s.Store.Tags.Delete(ctx, nil, id)
	s.RecordEvent(ctx, account, id, "subnet", "deleted", "Subnet "+id+" deleted", nil)
	return nil
}

// ---- Route tables

func (s *NetworkService) CreateRouteTable(ctx context.Context, account, vpcID, name string) (*repositories.RouteTable, error) {
	v, err := s.GetVPC(ctx, account, vpcID)
	if err != nil {
		return nil, err
	}
	rt := &repositories.RouteTable{RouteTableID: ids.New("rtb"), AccountID: account, VPCID: vpcID, Name: name}
	err = s.Store.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.repo().InsertRouteTable(ctx, tx, rt); err != nil {
			return err
		}
		if err := s.repo().PutRoute(ctx, tx, rt.RouteTableID, repositories.Route{DestinationCIDR: v.CIDRBlock, TargetID: "local"}); err != nil {
			return err
		}
		return s.Store.Tags.Replace(ctx, tx, account, rt.RouteTableID, withName(nil, name))
	})
	if err != nil {
		return nil, err
	}
	_, _ = s.Engine.Register(ctx, rt.RouteTableID, simulator.RegisterRequest{Kind: "route_table", Owner: account, DependsOn: []string{vpcID}})
	s.RecordEvent(ctx, account, rt.RouteTableID, "route_table", "created", "Route table "+rt.RouteTableID+" created in "+vpcID, nil)
	return s.GetRouteTable(ctx, account, rt.RouteTableID)
}

func (s *NetworkService) GetRouteTable(ctx context.Context, account, id string) (*repositories.RouteTable, error) {
	rt, err := s.repo().GetRouteTable(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "route table "+id)
	}
	rt.Tags, _ = s.Store.Tags.Get(ctx, id)
	return rt, nil
}

func (s *NetworkService) ListRouteTables(ctx context.Context, account, vpcID string) ([]repositories.RouteTable, error) {
	l, err := s.repo().ListRouteTables(ctx, account, vpcID)
	if err != nil {
		return nil, err
	}
	return l, s.attachTags(ctx, len(l), func(i int) (string, *map[string]string) { return l[i].RouteTableID, &l[i].Tags })
}

func (s *NetworkService) DeleteRouteTable(ctx context.Context, account, id string) error {
	rt, err := s.GetRouteTable(ctx, account, id)
	if err != nil {
		return err
	}
	if rt.IsMain {
		return apierr.Dependency("the main route table of a VPC cannot be deleted")
	}
	if len(rt.SubnetIDs) > 0 {
		return apierr.Dependency("route table %s is associated with subnets: %s", id, strings.Join(rt.SubnetIDs, ", "))
	}
	_ = s.Engine.Deregister(ctx, id)
	_ = s.Store.Tags.Delete(ctx, nil, id)
	return s.repo().DeleteRouteTable(ctx, id)
}

// AddRoute validates the target (IGW attached to the same VPC, or a NAT gateway in the VPC).
func (s *NetworkService) AddRoute(ctx context.Context, account, rtID, dest, target string) error {
	rt, err := s.GetRouteTable(ctx, account, rtID)
	if err != nil {
		return err
	}
	p, err := ParseCIDR(dest)
	if err != nil {
		return err
	}
	v, err := s.GetVPC(ctx, account, rt.VPCID)
	if err != nil {
		return err
	}
	if p.String() == v.CIDRBlock || target == "local" {
		return apierr.Validation("the local route is managed automatically and cannot be replaced")
	}
	switch {
	case strings.HasPrefix(target, "igw-"):
		g, err := s.repo().GetIGW(ctx, account, target)
		if err != nil {
			return RepoErr(err, "internet gateway "+target)
		}
		if g.VPCID == nil || *g.VPCID != rt.VPCID {
			return apierr.Validation("internet gateway %s is not attached to %s", target, rt.VPCID)
		}
	case strings.HasPrefix(target, "nat-"):
		n, err := s.repo().GetNAT(ctx, account, target)
		if err != nil {
			return RepoErr(err, "NAT gateway "+target)
		}
		if n.VPCID != rt.VPCID {
			return apierr.Validation("NAT gateway %s is in a different VPC", target)
		}
		if n.State == "deleting" || n.State == "deleted" {
			return apierr.Validation("NAT gateway %s is %s", target, n.State)
		}
	default:
		return apierr.Validation("route target must be an internet gateway (igw-…) or NAT gateway (nat-…)")
	}
	if err := s.repo().PutRoute(ctx, nil, rtID, repositories.Route{DestinationCIDR: p.String(), TargetID: target}); err != nil {
		return err
	}
	s.RecordEvent(ctx, account, rtID, "route_table", "route_added", "Route "+p.String()+" → "+target+" added to "+rtID, nil)
	return nil
}

func (s *NetworkService) DeleteRoute(ctx context.Context, account, rtID, dest string) error {
	rt, err := s.GetRouteTable(ctx, account, rtID)
	if err != nil {
		return err
	}
	for _, r := range rt.Routes {
		if r.DestinationCIDR == dest && r.TargetID == "local" {
			return apierr.Validation("the local route cannot be deleted")
		}
	}
	if err := s.repo().DeleteRoute(ctx, rtID, dest); err != nil {
		return RepoErr(err, "route "+dest)
	}
	s.RecordEvent(ctx, account, rtID, "route_table", "route_deleted", "Route "+dest+" removed from "+rtID, nil)
	return nil
}

func (s *NetworkService) AssociateRouteTable(ctx context.Context, account, rtID, subnetID string) error {
	rt, err := s.GetRouteTable(ctx, account, rtID)
	if err != nil {
		return err
	}
	sn, err := s.GetSubnet(ctx, account, subnetID)
	if err != nil {
		return err
	}
	if sn.VPCID != rt.VPCID {
		return apierr.Validation("subnet and route table must be in the same VPC")
	}
	target := rtID
	if rt.IsMain {
		target = "" // implicit association with main
	}
	return s.repo().AssociateRouteTable(ctx, subnetID, target)
}

// ---- Internet gateways

func (s *NetworkService) CreateIGW(ctx context.Context, account, name string, tags map[string]string) (*repositories.InternetGateway, error) {
	if err := ValidateTags(tags); err != nil {
		return nil, err
	}
	g := &repositories.InternetGateway{InternetGatewayID: ids.New("igw"), AccountID: account, Name: name, State: "detached"}
	if _, err := s.Engine.Register(ctx, g.InternetGatewayID, simulator.RegisterRequest{Kind: "internet_gateway", Owner: account}); err != nil {
		return nil, EngineErr(err)
	}
	if err := s.repo().InsertIGW(ctx, nil, g); err != nil {
		return nil, err
	}
	g.Tags = withName(tags, name)
	_ = s.Store.Tags.Replace(ctx, nil, account, g.InternetGatewayID, g.Tags)
	s.RecordEvent(ctx, account, g.InternetGatewayID, "internet_gateway", "created", "Internet gateway "+g.InternetGatewayID+" created", nil)
	return g, nil
}

func (s *NetworkService) GetIGW(ctx context.Context, account, id string) (*repositories.InternetGateway, error) {
	g, err := s.repo().GetIGW(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "internet gateway "+id)
	}
	g.Tags, _ = s.Store.Tags.Get(ctx, id)
	return g, nil
}

func (s *NetworkService) ListIGWs(ctx context.Context, account string) ([]repositories.InternetGateway, error) {
	l, err := s.repo().ListIGWs(ctx, account)
	if err != nil {
		return nil, err
	}
	return l, s.attachTags(ctx, len(l), func(i int) (string, *map[string]string) { return l[i].InternetGatewayID, &l[i].Tags })
}

func (s *NetworkService) AttachIGW(ctx context.Context, account, id, vpcID string) error {
	g, err := s.GetIGW(ctx, account, id)
	if err != nil {
		return err
	}
	if g.VPCID != nil {
		return apierr.New(409, "Resource.AlreadyAssociated", "internet gateway %s is already attached to %s", id, *g.VPCID)
	}
	if _, err := s.GetVPC(ctx, account, vpcID); err != nil {
		return err
	}
	igws, _ := s.repo().ListIGWs(ctx, account)
	for _, o := range igws {
		if o.VPCID != nil && *o.VPCID == vpcID {
			return apierr.Conflict("VPC %s already has internet gateway %s attached", vpcID, o.InternetGatewayID)
		}
	}
	if err := s.repo().SetIGWVPC(ctx, nil, id, &vpcID); err != nil {
		return err
	}
	_, _ = s.Engine.Register(ctx, id, simulator.RegisterRequest{Kind: "internet_gateway", Owner: account, DependsOn: []string{vpcID}})
	s.RecordEvent(ctx, account, id, "internet_gateway", "attached", "Internet gateway "+id+" attached to "+vpcID, nil)
	return nil
}

func (s *NetworkService) DetachIGW(ctx context.Context, account, id string) error {
	g, err := s.GetIGW(ctx, account, id)
	if err != nil {
		return err
	}
	if g.VPCID == nil {
		return apierr.Conflict("internet gateway %s is not attached", id)
	}
	// AWS refuses detach while instances in the VPC have public IPs mapped; we mirror that.
	var n int
	_ = s.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances WHERE vpc_id=? AND public_ip<>'' AND state<>'terminated'`, *g.VPCID).Scan(&n)
	if n > 0 {
		return apierr.Dependency("VPC %s has %d instance(s) with public addresses mapped; terminate them or remove their public IPs first", *g.VPCID, n)
	}
	if err := s.repo().SetIGWVPC(ctx, nil, id, nil); err != nil {
		return err
	}
	_, _ = s.Engine.Register(ctx, id, simulator.RegisterRequest{Kind: "internet_gateway", Owner: account})
	s.RecordEvent(ctx, account, id, "internet_gateway", "detached", "Internet gateway "+id+" detached from "+*g.VPCID, nil)
	return nil
}

func (s *NetworkService) DeleteIGW(ctx context.Context, account, id string) error {
	g, err := s.GetIGW(ctx, account, id)
	if err != nil {
		return err
	}
	if g.VPCID != nil {
		return apierr.Dependency("internet gateway %s is attached to %s; detach it first", id, *g.VPCID)
	}
	_ = s.Engine.Deregister(ctx, id)
	_ = s.Store.Tags.Delete(ctx, nil, id)
	if err := s.repo().DeleteIGW(ctx, id); err != nil {
		return err
	}
	s.RecordEvent(ctx, account, id, "internet_gateway", "deleted", "Internet gateway "+id+" deleted", nil)
	return nil
}

// ---- NAT gateways (lifecycle simulated by the engine)

func (s *NetworkService) CreateNAT(ctx context.Context, account, subnetID, name string) (*repositories.NATGateway, error) {
	sn, err := s.GetSubnet(ctx, account, subnetID)
	if err != nil {
		return nil, err
	}
	used, err := s.repo().UsedIPs(ctx, subnetID)
	if err != nil {
		return nil, err
	}
	ip, err := AllocateIP(netip.MustParsePrefix(sn.CIDRBlock), used)
	if err != nil {
		return nil, err
	}
	seq, err := s.NextSeq(ctx, "public_ip")
	if err != nil {
		return nil, err
	}
	n := &repositories.NATGateway{NATGatewayID: ids.New("nat"), AccountID: account, Name: name, SubnetID: subnetID, VPCID: sn.VPCID,
		State: "pending", PublicIP: PublicIP(seq), PrivateIP: ip}
	res, err := s.Engine.Register(ctx, n.NATGatewayID, simulator.RegisterRequest{Kind: "nat_gateway", Owner: account, DependsOn: []string{subnetID}})
	if err != nil {
		return nil, EngineErr(err)
	}
	n.State = res.State
	if err := s.repo().InsertNAT(ctx, n); err != nil {
		_ = s.Engine.Deregister(ctx, n.NATGatewayID)
		return nil, err
	}
	n.Tags = withName(nil, name)
	_ = s.Store.Tags.Replace(ctx, nil, account, n.NATGatewayID, n.Tags)
	s.RecordEvent(ctx, account, n.NATGatewayID, "nat_gateway", "created", "NAT gateway "+n.NATGatewayID+" creating in "+subnetID, nil)
	return n, nil
}

func (s *NetworkService) GetNAT(ctx context.Context, account, id string) (*repositories.NATGateway, error) {
	n, err := s.repo().GetNAT(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "NAT gateway "+id)
	}
	n.Tags, _ = s.Store.Tags.Get(ctx, id)
	return n, nil
}

func (s *NetworkService) ListNATs(ctx context.Context, account string) ([]repositories.NATGateway, error) {
	l, err := s.repo().ListNATs(ctx, account)
	if err != nil {
		return nil, err
	}
	return l, s.attachTags(ctx, len(l), func(i int) (string, *map[string]string) { return l[i].NATGatewayID, &l[i].Tags })
}

func (s *NetworkService) DeleteNAT(ctx context.Context, account, id string) (*repositories.NATGateway, error) {
	n, err := s.GetNAT(ctx, account, id)
	if err != nil {
		return nil, err
	}
	if n.State == "deleted" || n.State == "deleting" {
		return n, nil
	}
	res, err := s.Engine.Action(ctx, id, "delete", "User initiated")
	if err != nil {
		return nil, EngineErr(err)
	}
	n.State = res.State
	_ = s.repo().SetNATState(ctx, id, res.State)
	return n, nil
}

// OnNATState is called by the engine event sync.
func (s *NetworkService) OnNATState(ctx context.Context, id, state, reason string) {
	n, err := s.repo().GetNATByID(ctx, id)
	if err != nil {
		return
	}
	_ = s.repo().SetNATState(ctx, id, state)
	if state == "deleted" {
		_ = s.Engine.Deregister(ctx, id)
	}
	s.RecordEvent(ctx, n.AccountID, id, "nat_gateway", "state_changed", "NAT gateway "+id+" is now "+state, map[string]string{"reason": reason})
}

// ---- Security groups

type CreateSGInput struct {
	GroupName   string            `json:"group_name"`
	Description string            `json:"description"`
	VPCID       string            `json:"vpc_id"`
	Tags        map[string]string `json:"tags"`
}

func (s *NetworkService) CreateSG(ctx context.Context, account string, in CreateSGInput) (*repositories.SecurityGroup, error) {
	if in.GroupName == "" || len(in.GroupName) > 255 {
		return nil, apierr.Validation("group_name is required (max 255 chars)")
	}
	if strings.EqualFold(in.GroupName, "default") {
		return nil, apierr.Validation("the name 'default' is reserved")
	}
	if in.VPCID == "" {
		v, _, err := s.EnsureDefaultVPC(ctx, account)
		if err != nil {
			return nil, err
		}
		in.VPCID = v.VPCID
	}
	if _, err := s.GetVPC(ctx, account, in.VPCID); err != nil {
		return nil, err
	}
	if err := ValidateTags(in.Tags); err != nil {
		return nil, err
	}
	g := &repositories.SecurityGroup{GroupID: ids.New("sg"), AccountID: account, VPCID: in.VPCID, GroupName: in.GroupName, Description: in.Description}
	err := s.Store.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.repo().InsertSG(ctx, tx, g); err != nil {
			return err
		}
		if err := s.repo().InsertSGRule(ctx, tx, &repositories.SGRule{RuleID: ids.New("sgr"), GroupID: g.GroupID, Direction: "egress", Protocol: "-1", FromPort: 0, ToPort: 65535, CIDR: "0.0.0.0/0", Description: "allow all outbound"}); err != nil {
			return err
		}
		return s.Store.Tags.Replace(ctx, tx, account, g.GroupID, in.Tags)
	})
	if err != nil {
		return nil, RepoErr(err, "security group "+in.GroupName)
	}
	if _, err := s.Engine.Register(ctx, g.GroupID, simulator.RegisterRequest{Kind: "security_group", Owner: account, DependsOn: []string{in.VPCID}}); err != nil {
		_ = s.repo().DeleteSG(ctx, g.GroupID)
		return nil, EngineErr(err)
	}
	s.RecordEvent(ctx, account, g.GroupID, "security_group", "created", "Security group "+g.GroupID+" ("+g.GroupName+") created", nil)
	return s.GetSG(ctx, account, g.GroupID)
}

func (s *NetworkService) GetSG(ctx context.Context, account, id string) (*repositories.SecurityGroup, error) {
	g, err := s.repo().GetSG(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "security group "+id)
	}
	g.Tags, _ = s.Store.Tags.Get(ctx, id)
	return g, nil
}

func (s *NetworkService) ListSGs(ctx context.Context, account, vpcID string) ([]repositories.SecurityGroup, error) {
	l, err := s.repo().ListSGs(ctx, account, vpcID)
	if err != nil {
		return nil, err
	}
	return l, s.attachTags(ctx, len(l), func(i int) (string, *map[string]string) { return l[i].GroupID, &l[i].Tags })
}

func (s *NetworkService) DeleteSG(ctx context.Context, account, id string) error {
	g, err := s.GetSG(ctx, account, id)
	if err != nil {
		return err
	}
	if g.IsDefault {
		return apierr.Validation("the default security group cannot be deleted")
	}
	n, err := s.repo().SGInUse(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return apierr.Dependency("security group %s is in use by %d network interface(s) or rule(s)", id, n)
	}
	_ = s.Engine.Deregister(ctx, id)
	_ = s.Store.Tags.Delete(ctx, nil, id)
	if err := s.repo().DeleteSG(ctx, id); err != nil {
		return RepoErr(err, "security group")
	}
	s.RecordEvent(ctx, account, id, "security_group", "deleted", "Security group "+id+" deleted", nil)
	return nil
}

type SGRuleInput struct {
	Direction             string `json:"direction"`
	Protocol              string `json:"protocol"`
	FromPort              int    `json:"from_port"`
	ToPort                int    `json:"to_port"`
	CIDR                  string `json:"cidr"`
	SourceSecurityGroupID string `json:"source_security_group_id"`
	Description           string `json:"description"`
}

func normalizeProtoPorts(proto string, from, to int) (string, int, int, error) {
	proto = strings.ToLower(proto)
	switch proto {
	case "all":
		proto = "-1"
	case "-1", "tcp", "udp", "icmp":
	default:
		return "", 0, 0, apierr.Validation("protocol must be tcp, udp, icmp or -1 (all)")
	}
	if proto == "-1" || proto == "icmp" {
		return proto, 0, 65535, nil
	}
	if from < 0 || to > 65535 || from > to {
		return "", 0, 0, apierr.Validation("ports must satisfy 0 <= from_port <= to_port <= 65535")
	}
	return proto, from, to, nil
}

func (s *NetworkService) AddSGRule(ctx context.Context, account, groupID string, in SGRuleInput) (*repositories.SecurityGroup, error) {
	g, err := s.GetSG(ctx, account, groupID)
	if err != nil {
		return nil, err
	}
	if in.Direction != "ingress" && in.Direction != "egress" {
		return nil, apierr.Validation("direction must be ingress or egress")
	}
	proto, from, to, err := normalizeProtoPorts(in.Protocol, in.FromPort, in.ToPort)
	if err != nil {
		return nil, err
	}
	if (in.CIDR == "") == (in.SourceSecurityGroupID == "") {
		return nil, apierr.Validation("specify exactly one of cidr or source_security_group_id")
	}
	if in.CIDR != "" {
		p, err := ParseCIDR(in.CIDR)
		if err != nil {
			return nil, err
		}
		in.CIDR = p.String()
	} else {
		src, err := s.GetSG(ctx, account, in.SourceSecurityGroupID)
		if err != nil {
			return nil, err
		}
		if src.VPCID != g.VPCID {
			return nil, apierr.Validation("source security group must be in the same VPC")
		}
	}
	list := g.Ingress
	if in.Direction == "egress" {
		list = g.Egress
	}
	for _, r := range list {
		if r.Protocol == proto && r.FromPort == from && r.ToPort == to && r.CIDR == in.CIDR && r.SourceSecurityGroupID == in.SourceSecurityGroupID {
			return nil, apierr.New(409, "InvalidPermission.Duplicate", "the specified rule already exists (%s)", r.RuleID)
		}
	}
	if len(list) >= 60 {
		return nil, apierr.Quota("security groups are limited to 60 %s rules", in.Direction)
	}
	rule := &repositories.SGRule{RuleID: ids.New("sgr"), GroupID: groupID, Direction: in.Direction, Protocol: proto, FromPort: from, ToPort: to,
		CIDR: in.CIDR, SourceSecurityGroupID: in.SourceSecurityGroupID, Description: in.Description}
	if err := s.repo().InsertSGRule(ctx, nil, rule); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, groupID, "security_group", "rule_added", in.Direction+" rule "+proto+" "+portRange(from, to)+" added to "+groupID, rule)
	return s.GetSG(ctx, account, groupID)
}

func portRange(from, to int) string {
	if from == to {
		return itoa(from)
	}
	return itoa(from) + "-" + itoa(to)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func (s *NetworkService) DeleteSGRule(ctx context.Context, account, groupID, ruleID string) error {
	if _, err := s.GetSG(ctx, account, groupID); err != nil {
		return err
	}
	if err := s.repo().DeleteSGRule(ctx, groupID, ruleID); err != nil {
		return RepoErr(err, "rule "+ruleID)
	}
	s.RecordEvent(ctx, account, groupID, "security_group", "rule_removed", "Rule "+ruleID+" removed from "+groupID, nil)
	return nil
}

// ---- Network ACLs

func (s *NetworkService) CreateACL(ctx context.Context, account, vpcID, name string) (*repositories.NetworkACL, error) {
	if _, err := s.GetVPC(ctx, account, vpcID); err != nil {
		return nil, err
	}
	a := &repositories.NetworkACL{NetworkACLID: ids.New("acl"), AccountID: account, VPCID: vpcID, Name: name}
	if err := s.repo().InsertACL(ctx, nil, a); err != nil {
		return nil, err
	}
	_ = s.Store.Tags.Replace(ctx, nil, account, a.NetworkACLID, withName(nil, name))
	_, _ = s.Engine.Register(ctx, a.NetworkACLID, simulator.RegisterRequest{Kind: "network_acl", Owner: account, DependsOn: []string{vpcID}})
	s.RecordEvent(ctx, account, a.NetworkACLID, "network_acl", "created", "Network ACL "+a.NetworkACLID+" created (denies all until rules are added)", nil)
	return s.GetACL(ctx, account, a.NetworkACLID)
}

func (s *NetworkService) GetACL(ctx context.Context, account, id string) (*repositories.NetworkACL, error) {
	a, err := s.repo().GetACL(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "network ACL "+id)
	}
	a.Tags, _ = s.Store.Tags.Get(ctx, id)
	return a, nil
}

func (s *NetworkService) ListACLs(ctx context.Context, account, vpcID string) ([]repositories.NetworkACL, error) {
	l, err := s.repo().ListACLs(ctx, account, vpcID)
	if err != nil {
		return nil, err
	}
	return l, s.attachTags(ctx, len(l), func(i int) (string, *map[string]string) { return l[i].NetworkACLID, &l[i].Tags })
}

func (s *NetworkService) DeleteACL(ctx context.Context, account, id string) error {
	a, err := s.GetACL(ctx, account, id)
	if err != nil {
		return err
	}
	if a.IsDefault {
		return apierr.Validation("the default network ACL cannot be deleted")
	}
	if len(a.SubnetIDs) > 0 {
		return apierr.Dependency("network ACL %s is associated with subnets: %s", id, strings.Join(a.SubnetIDs, ", "))
	}
	_ = s.Engine.Deregister(ctx, id)
	_ = s.Store.Tags.Delete(ctx, nil, id)
	return s.repo().DeleteACL(ctx, id)
}

func (s *NetworkService) PutACLEntry(ctx context.Context, account, aclID string, e repositories.ACLEntry) (*repositories.NetworkACL, error) {
	if _, err := s.GetACL(ctx, account, aclID); err != nil {
		return nil, err
	}
	if e.RuleNumber < 1 || e.RuleNumber > 32766 {
		return nil, apierr.Validation("rule_number must be 1-32766")
	}
	if e.Action != "allow" && e.Action != "deny" {
		return nil, apierr.Validation("action must be allow or deny")
	}
	proto, from, to, err := normalizeProtoPorts(e.Protocol, e.FromPort, e.ToPort)
	if err != nil {
		return nil, err
	}
	p, err := ParseCIDR(e.CIDR)
	if err != nil {
		return nil, err
	}
	e.Protocol, e.FromPort, e.ToPort, e.CIDR = proto, from, to, p.String()
	if err := s.repo().PutACLEntry(ctx, nil, aclID, e); err != nil {
		return nil, err
	}
	dir := "inbound"
	if e.Egress {
		dir = "outbound"
	}
	s.RecordEvent(ctx, account, aclID, "network_acl", "entry_put", dir+" rule #"+itoa(e.RuleNumber)+" "+e.Action+" "+e.CIDR, e)
	return s.GetACL(ctx, account, aclID)
}

func (s *NetworkService) DeleteACLEntry(ctx context.Context, account, aclID string, n int, egress bool) error {
	if _, err := s.GetACL(ctx, account, aclID); err != nil {
		return err
	}
	return RepoErr(s.repo().DeleteACLEntry(ctx, aclID, n, egress), "ACL entry")
}

func (s *NetworkService) AssociateACL(ctx context.Context, account, aclID, subnetID string) error {
	a, err := s.GetACL(ctx, account, aclID)
	if err != nil {
		return err
	}
	sn, err := s.GetSubnet(ctx, account, subnetID)
	if err != nil {
		return err
	}
	if sn.VPCID != a.VPCID {
		return apierr.Validation("subnet and network ACL must be in the same VPC")
	}
	return s.repo().AssociateACL(ctx, subnetID, aclID)
}

// ---- ENIs

type CreateENIInput struct {
	SubnetID         string   `json:"subnet_id"`
	Description      string   `json:"description"`
	SecurityGroupIDs []string `json:"security_group_ids"`
	PrivateIP        string   `json:"private_ip"`
}

func (s *NetworkService) ResolveSGs(ctx context.Context, account, vpcID string, groups []string) ([]string, error) {
	if len(groups) == 0 {
		d, err := s.repo().DefaultSG(ctx, vpcID)
		if err != nil {
			return nil, err
		}
		return []string{d.GroupID}, nil
	}
	if len(groups) > 5 {
		return nil, apierr.Validation("at most 5 security groups per network interface")
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range groups {
		if seen[g] {
			continue
		}
		seen[g] = true
		sg, err := s.GetSG(ctx, account, g)
		if err != nil {
			return nil, err
		}
		if sg.VPCID != vpcID {
			return nil, apierr.Validation("security group %s belongs to %s, not %s", g, sg.VPCID, vpcID)
		}
		out = append(out, g)
	}
	return out, nil
}

func (s *NetworkService) CreateENI(ctx context.Context, account string, in CreateENIInput) (*repositories.NetworkInterface, error) {
	sn, err := s.GetSubnet(ctx, account, in.SubnetID)
	if err != nil {
		return nil, err
	}
	groups, err := s.ResolveSGs(ctx, account, sn.VPCID, in.SecurityGroupIDs)
	if err != nil {
		return nil, err
	}
	used, err := s.repo().UsedIPs(ctx, sn.SubnetID)
	if err != nil {
		return nil, err
	}
	pfx := netip.MustParsePrefix(sn.CIDRBlock)
	ip := in.PrivateIP
	if ip == "" {
		if ip, err = AllocateIP(pfx, used); err != nil {
			return nil, err
		}
	} else {
		a, err := netip.ParseAddr(ip)
		if err != nil || !pfx.Contains(a) {
			return nil, apierr.Validation("private_ip %s is not in subnet %s", ip, pfx)
		}
		for _, u := range used {
			if u == ip {
				return nil, apierr.Conflict("address %s is already in use", ip)
			}
		}
	}
	e := &repositories.NetworkInterface{NetworkInterfaceID: ids.New("eni"), AccountID: account, SubnetID: sn.SubnetID, VPCID: sn.VPCID,
		PrivateIP: ip, Description: in.Description, SecurityGroupIDs: groups}
	if err := s.repo().InsertENI(ctx, nil, e); err != nil {
		return nil, err
	}
	_, _ = s.Engine.Register(ctx, e.NetworkInterfaceID, simulator.RegisterRequest{Kind: "network_interface", Owner: account, DependsOn: []string{sn.SubnetID}})
	return s.repo().GetENI(ctx, account, e.NetworkInterfaceID)
}

func (s *NetworkService) ListENIs(ctx context.Context, account string) ([]repositories.NetworkInterface, error) {
	return s.repo().ListENIs(ctx, account)
}

func (s *NetworkService) DeleteENI(ctx context.Context, account, id string) error {
	e, err := s.repo().GetENI(ctx, account, id)
	if err != nil {
		return RepoErr(err, "network interface "+id)
	}
	if e.Attachment != nil {
		return apierr.Dependency("network interface %s is attached to %s", id, e.Attachment.InstanceID)
	}
	_ = s.Engine.Deregister(ctx, id)
	return s.repo().DeleteENI(ctx, nil, id)
}

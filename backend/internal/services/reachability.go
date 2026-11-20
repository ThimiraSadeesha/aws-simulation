package services

import (
	"context"
	"net/netip"
	"strings"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/repositories"
)

// Topology snapshot types follow docs/engine-protocol.md §5.
type topoRule struct {
	Protocol string  `json:"protocol"`
	FromPort int     `json:"from_port"`
	ToPort   int     `json:"to_port"`
	CIDR     string  `json:"cidr"`
	SourceSG *string `json:"source_sg"`
}

type topoEndpoint struct {
	ID               string   `json:"id"`
	SubnetID         string   `json:"subnet_id"`
	PrivateIP        string   `json:"private_ip"`
	PublicIP         string   `json:"public_ip"`
	SecurityGroupIDs []string `json:"security_group_ids"`
	State            string   `json:"state"`
}

// ReachabilityInput asks whether traffic can flow between two endpoints.
type ReachabilityInput struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Protocol    string `json:"protocol"`
	Port        int    `json:"port"`
}

// BuildTopology assembles the engine snapshot for every VPC in the account.
func (s *NetworkService) BuildTopology(ctx context.Context, account string) (map[string]any, error) {
	vpcs, err := s.repo().ListVPCs(ctx, account)
	if err != nil {
		return nil, err
	}
	subs, err := s.repo().ListSubnets(ctx, account, "")
	if err != nil {
		return nil, err
	}
	rts, err := s.repo().ListRouteTables(ctx, account, "")
	if err != nil {
		return nil, err
	}
	igws, err := s.repo().ListIGWs(ctx, account)
	if err != nil {
		return nil, err
	}
	nats, err := s.repo().ListNATs(ctx, account)
	if err != nil {
		return nil, err
	}
	sgs, err := s.repo().ListSGs(ctx, account, "")
	if err != nil {
		return nil, err
	}
	acls, err := s.repo().ListACLs(ctx, account, "")
	if err != nil {
		return nil, err
	}
	insts, err := s.Store.EC2.ListInstances(ctx, repositories.InstanceFilter{AccountID: account})
	if err != nil {
		return nil, err
	}
	var tv, ts, trt, tigw, tnat, tsg, tacl []map[string]any
	for _, v := range vpcs {
		tv = append(tv, map[string]any{"id": v.VPCID, "cidr": v.CIDRBlock})
	}
	for _, sn := range subs {
		tv := map[string]any{"id": sn.SubnetID, "vpc_id": sn.VPCID, "cidr": sn.CIDRBlock, "route_table_id": sn.RouteTableID, "network_acl_id": sn.NetworkACLID}
		ts = append(ts, tv)
	}
	for _, rt := range rts {
		routes := []map[string]string{}
		for _, r := range rt.Routes {
			if r.State == "active" {
				routes = append(routes, map[string]string{"destination": r.DestinationCIDR, "target": r.TargetID})
			}
		}
		trt = append(trt, map[string]any{"id": rt.RouteTableID, "vpc_id": rt.VPCID, "routes": routes})
	}
	for _, g := range igws {
		if g.VPCID != nil {
			tigw = append(tigw, map[string]any{"id": g.InternetGatewayID, "vpc_id": *g.VPCID})
		}
	}
	for _, n := range nats {
		tnat = append(tnat, map[string]any{"id": n.NATGatewayID, "subnet_id": n.SubnetID, "state": n.State, "public_ip": n.PublicIP})
	}
	conv := func(rules []repositories.SGRule) []topoRule {
		out := []topoRule{}
		for _, r := range rules {
			tr := topoRule{Protocol: r.Protocol, FromPort: r.FromPort, ToPort: r.ToPort, CIDR: r.CIDR}
			if r.SourceSecurityGroupID != "" {
				x := r.SourceSecurityGroupID
				tr.SourceSG = &x
			}
			out = append(out, tr)
		}
		return out
	}
	for _, g := range sgs {
		tsg = append(tsg, map[string]any{"id": g.GroupID, "vpc_id": g.VPCID, "ingress": conv(g.Ingress), "egress": conv(g.Egress)})
	}
	for _, a := range acls {
		entries := []map[string]any{}
		for _, e := range a.Entries {
			entries = append(entries, map[string]any{"rule_number": e.RuleNumber, "egress": e.Egress, "protocol": e.Protocol, "from_port": e.FromPort, "to_port": e.ToPort, "cidr": e.CIDR, "action": e.Action})
		}
		tacl = append(tacl, map[string]any{"id": a.NetworkACLID, "vpc_id": a.VPCID, "entries": entries})
	}
	eps := []topoEndpoint{}
	for _, i := range insts {
		if i.State == "terminated" {
			continue
		}
		eps = append(eps, topoEndpoint{ID: i.InstanceID, SubnetID: i.SubnetID, PrivateIP: i.PrivateIP, PublicIP: i.PublicIP, SecurityGroupIDs: i.SecurityGroupIDs, State: i.State})
	}
	nz := func(x []map[string]any) []map[string]any {
		if x == nil {
			return []map[string]any{}
		}
		return x
	}
	return map[string]any{
		"vpcs": nz(tv), "subnets": nz(ts), "route_tables": nz(trt), "internet_gateways": nz(tigw),
		"nat_gateways": nz(tnat), "security_groups": nz(tsg), "network_acls": nz(tacl), "endpoints": eps,
	}, nil
}

// Reachability asks the engine to evaluate a probe over the account's current topology.
func (s *NetworkService) Reachability(ctx context.Context, account string, in ReachabilityInput) (map[string]any, error) {
	if in.Source == "" || in.Destination == "" {
		return nil, apierr.Validation("source and destination are required")
	}
	if in.Protocol == "" {
		in.Protocol = "tcp"
	}
	in.Protocol = strings.ToLower(in.Protocol)
	if in.Protocol == "all" {
		in.Protocol = "-1"
	}
	switch in.Protocol {
	case "tcp", "udp", "icmp", "-1":
	default:
		return nil, apierr.Validation("protocol must be tcp, udp, icmp or -1")
	}
	if in.Port < 0 || in.Port > 65535 {
		return nil, apierr.Validation("port must be 0-65535")
	}
	for _, ep := range []string{in.Source, in.Destination} {
		if ep == "internet" {
			continue
		}
		if strings.HasPrefix(ep, "i-") {
			if _, err := s.Store.EC2.GetInstance(ctx, account, ep); err != nil {
				return nil, RepoErr(err, "instance "+ep)
			}
			continue
		}
		if _, err := netip.ParseAddr(ep); err != nil {
			return nil, apierr.Validation("endpoint %q must be an instance id, 'internet' or an IPv4 address", ep)
		}
	}
	topo, err := s.BuildTopology(ctx, account)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.Engine.Raw(ctx, "POST", "/v1/network/reachability", map[string]any{"topology": topo, "probe": in}, &out)
	if err != nil {
		return nil, EngineErr(err)
	}
	return out, nil
}

// VPCTopology returns the data for the console's topology diagram.
func (s *NetworkService) VPCTopology(ctx context.Context, account, vpcID string) (map[string]any, error) {
	v, err := s.GetVPC(ctx, account, vpcID)
	if err != nil {
		return nil, err
	}
	subs, err := s.ListSubnets(ctx, account, vpcID)
	if err != nil {
		return nil, err
	}
	rts, err := s.ListRouteTables(ctx, account, vpcID)
	if err != nil {
		return nil, err
	}
	igws, _ := s.ListIGWs(ctx, account)
	var vIGWs []repositories.InternetGateway
	for _, g := range igws {
		if g.VPCID != nil && *g.VPCID == vpcID {
			vIGWs = append(vIGWs, g)
		}
	}
	nats, _ := s.ListNATs(ctx, account)
	var vNATs []repositories.NATGateway
	for _, n := range nats {
		if n.VPCID == vpcID && n.State != "deleted" {
			vNATs = append(vNATs, n)
		}
	}
	insts, _ := s.Store.EC2.ListInstances(ctx, repositories.InstanceFilter{AccountID: account, VPCID: vpcID})
	type ti struct {
		InstanceID string `json:"instance_id"`
		Name       string `json:"name"`
		State      string `json:"state"`
		SubnetID   string `json:"subnet_id"`
		PrivateIP  string `json:"private_ip"`
		PublicIP   string `json:"public_ip"`
	}
	tis := []ti{}
	for _, i := range insts {
		if i.State != "terminated" {
			tis = append(tis, ti{i.InstanceID, i.Name, i.State, i.SubnetID, i.PrivateIP, i.PublicIP})
		}
	}
	if vIGWs == nil {
		vIGWs = []repositories.InternetGateway{}
	}
	if vNATs == nil {
		vNATs = []repositories.NATGateway{}
	}
	var lbs []map[string]any
	if TopologyLBs != nil {
		lbs = TopologyLBs(ctx, account, vpcID)
	}
	if lbs == nil {
		lbs = []map[string]any{}
	}
	return map[string]any{"vpc": v, "subnets": subs, "route_tables": rts, "internet_gateways": vIGWs, "nat_gateways": vNATs, "instances": tis, "load_balancers": lbs}, nil
}

// TopologyLBs is set by the ELB module to contribute load balancers to topology views.
var TopologyLBs func(ctx context.Context, account, vpcID string) []map[string]any

// ResolveDNS implements the simulated VPC resolver.
func (s *NetworkService) ResolveDNS(ctx context.Context, account, name, vpcID string) (map[string]any, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" {
		return nil, apierr.Validation("name is required")
	}
	answers := []map[string]any{}
	source := "vpc-private"
	insts, err := s.Store.EC2.ListInstances(ctx, repositories.InstanceFilter{AccountID: account})
	if err != nil {
		return nil, err
	}
	for _, i := range insts {
		if i.State == "terminated" || (vpcID != "" && i.VPCID != vpcID) {
			continue
		}
		if strings.EqualFold(i.PrivateDNS, name) {
			answers = append(answers, map[string]any{"type": "A", "value": i.PrivateIP, "ttl": 60})
		}
		if i.Name != "" && strings.EqualFold(strings.ToLower(i.Name)+"."+i.VPCID+".cloudsim.internal", name) {
			v, _ := s.repo().GetVPC(ctx, account, i.VPCID)
			if v != nil && v.EnableDNSHostnames {
				answers = append(answers, map[string]any{"type": "A", "value": i.PrivateIP, "ttl": 60})
			}
		}
	}
	for _, res := range ExternalResolvers {
		if len(answers) > 0 {
			break
		}
		if a, src := res(ctx, account, name, vpcID); len(a) > 0 {
			answers, source = a, src
		}
	}
	if len(answers) == 0 {
		return map[string]any{"name": name, "answers": answers, "source": "none", "status": "NXDOMAIN"}, nil
	}
	return map[string]any{"name": name, "answers": answers, "source": source, "status": "NOERROR"}, nil
}

// ExternalResolvers let other modules (Route 53 zones, ELB DNS names) answer DNS queries.
// Register during startup only.
var ExternalResolvers []func(ctx context.Context, account, name, vpcID string) ([]map[string]any, string)

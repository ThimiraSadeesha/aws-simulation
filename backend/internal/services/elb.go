package services

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

// ELBService implements Elastic Load Balancing (target groups, load balancers, listeners, health, traffic simulation).
type ELBService struct {
	*Deps
	EC2  *EC2Service
	Net  *NetworkService
	Repo repositories.ELBRepo
}

func NewELBService(d *Deps, ec2 *EC2Service, n *NetworkService) *ELBService {
	return &ELBService{Deps: d, EC2: ec2, Net: n, Repo: repositories.NewELBRepo(d.Store.DB)}
}

var (
	elbNameRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,30}[A-Za-z0-9])?$`)
	elbProtos   = map[string]bool{"HTTP": true, "HTTPS": true, "TCP": true}
	elbAlgos    = map[string]bool{"round_robin": true, "least_outstanding_requests": true, "weighted_random": true}
	hcPathRe    = regexp.MustCompile(`^/[\x21-\x7e]{0,1023}$`)
	lbSchemes   = map[string]bool{"internet-facing": true, "internal": true}
	defaultPort = map[string]int{"HTTP": 80, "HTTPS": 443, "TCP": 80}
)

// TargetStatus is a registered target with its computed health.
type TargetStatus struct {
	ID     string `json:"id"`
	Port   int    `json:"port"`
	Weight int    `json:"weight"`
	Health string `json:"health"`
	Reason string `json:"reason"`
}

type TargetGroupView struct {
	repositories.TargetGroup
	ARN             string         `json:"arn"`
	Targets         []TargetStatus `json:"targets"`
	LoadBalancerIDs []string       `json:"load_balancer_ids"`
}

type LoadBalancerView struct {
	repositories.LoadBalancer
	ARN       string                  `json:"arn"`
	Listeners []repositories.Listener `json:"listeners"`
}

func (s *ELBService) tgARN(acct, id string) string {
	return auth.ARN("elasticloadbalancing", acct, "targetgroup/"+id)
}
func (s *ELBService) lbARN(acct, id string) string {
	return auth.ARN("elasticloadbalancing", acct, "loadbalancer/"+id)
}

// ---- Target groups

type CreateTargetGroupInput struct {
	Name        string                    `json:"name"`
	Protocol    string                    `json:"protocol"`
	Port        int                       `json:"port"`
	VPCID       string                    `json:"vpc_id"`
	Algorithm   string                    `json:"algorithm"`
	HealthCheck *repositories.HealthCheck `json:"health_check"`
}

func normalizeHC(hc *repositories.HealthCheck, base repositories.HealthCheck) (repositories.HealthCheck, error) {
	out := base
	if hc != nil {
		if hc.Path != "" {
			out.Path = hc.Path
		}
		if hc.IntervalS != 0 {
			out.IntervalS = hc.IntervalS
		}
		if hc.HealthyThreshold != 0 {
			out.HealthyThreshold = hc.HealthyThreshold
		}
		if hc.UnhealthyThreshold != 0 {
			out.UnhealthyThreshold = hc.UnhealthyThreshold
		}
	}
	if !hcPathRe.MatchString(out.Path) {
		return out, apierr.Validation("health_check.path must start with '/' (max 1024 chars)")
	}
	if out.IntervalS < 5 || out.IntervalS > 300 {
		return out, apierr.Validation("health_check.interval_s must be between 5 and 300")
	}
	if out.HealthyThreshold < 2 || out.HealthyThreshold > 10 || out.UnhealthyThreshold < 2 || out.UnhealthyThreshold > 10 {
		return out, apierr.Validation("health check thresholds must be between 2 and 10")
	}
	return out, nil
}

func (s *ELBService) CreateTargetGroup(ctx context.Context, account string, in CreateTargetGroupInput) (*TargetGroupView, error) {
	if !elbNameRe.MatchString(in.Name) {
		return nil, apierr.Validation("name must be 1-32 alphanumeric characters or hyphens, not starting or ending with a hyphen")
	}
	in.Protocol = strings.ToUpper(in.Protocol)
	if in.Protocol == "" {
		in.Protocol = "HTTP"
	}
	if !elbProtos[in.Protocol] {
		return nil, apierr.Validation("protocol must be HTTP, HTTPS or TCP")
	}
	if in.Port == 0 {
		in.Port = defaultPort[in.Protocol]
	}
	if in.Port < 1 || in.Port > 65535 {
		return nil, apierr.Validation("port must be between 1 and 65535")
	}
	if in.Algorithm == "" {
		in.Algorithm = "round_robin"
	}
	if !elbAlgos[in.Algorithm] {
		return nil, apierr.Validation("algorithm must be round_robin, least_outstanding_requests or weighted_random")
	}
	if in.VPCID == "" {
		return nil, apierr.Validation("vpc_id is required")
	}
	if _, err := s.Net.GetVPC(ctx, account, in.VPCID); err != nil {
		return nil, err
	}
	hc, err := normalizeHC(in.HealthCheck, repositories.HealthCheck{Path: "/", IntervalS: 30, HealthyThreshold: 2, UnhealthyThreshold: 2})
	if err != nil {
		return nil, err
	}
	tg := &repositories.TargetGroup{TargetGroupID: ids.New("tg"), AccountID: account, Name: in.Name, Protocol: in.Protocol, Port: in.Port,
		VPCID: in.VPCID, Algorithm: in.Algorithm, HealthCheck: hc}
	if err := s.Repo.InsertTG(ctx, tg); err != nil {
		return nil, RepoErr(err, "target group "+in.Name)
	}
	if _, err := s.Engine.Register(ctx, tg.TargetGroupID, simulator.RegisterRequest{Kind: "target_group", Owner: account, DependsOn: []string{in.VPCID}}); err != nil {
		_ = s.Repo.DeleteTG(ctx, tg.TargetGroupID)
		return nil, EngineErr(err)
	}
	s.RecordEvent(ctx, account, tg.TargetGroupID, "target_group", "created", fmt.Sprintf("Target group %s (%s:%d) created in %s", in.Name, in.Protocol, in.Port, in.VPCID), nil)
	return s.GetTargetGroup(ctx, account, tg.TargetGroupID)
}

func (s *ELBService) getTG(ctx context.Context, account, id string) (*repositories.TargetGroup, error) {
	tg, err := s.Repo.GetTG(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "target group "+id)
	}
	return tg, nil
}

func (s *ELBService) view(ctx context.Context, tg *repositories.TargetGroup) (*TargetGroupView, error) {
	health, err := s.TargetHealth(ctx, tg)
	if err != nil {
		return nil, err
	}
	lbs, err := s.Repo.LBsOfTG(ctx, tg.TargetGroupID)
	if err != nil {
		return nil, err
	}
	return &TargetGroupView{TargetGroup: *tg, ARN: s.tgARN(tg.AccountID, tg.TargetGroupID), Targets: health, LoadBalancerIDs: lbs}, nil
}

func (s *ELBService) GetTargetGroup(ctx context.Context, account, id string) (*TargetGroupView, error) {
	tg, err := s.getTG(ctx, account, id)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, tg)
}

func (s *ELBService) ListTargetGroups(ctx context.Context, account string) ([]TargetGroupView, error) {
	l, err := s.Repo.ListTGs(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]TargetGroupView, 0, len(l))
	for i := range l {
		v, err := s.view(ctx, &l[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

type UpdateTargetGroupInput struct {
	Algorithm   *string                   `json:"algorithm"`
	HealthCheck *repositories.HealthCheck `json:"health_check"`
}

func (s *ELBService) UpdateTargetGroup(ctx context.Context, account, id string, in UpdateTargetGroupInput) (*TargetGroupView, error) {
	tg, err := s.getTG(ctx, account, id)
	if err != nil {
		return nil, err
	}
	if in.Algorithm != nil {
		if !elbAlgos[*in.Algorithm] {
			return nil, apierr.Validation("algorithm must be round_robin, least_outstanding_requests or weighted_random")
		}
		tg.Algorithm = *in.Algorithm
	}
	if tg.HealthCheck, err = normalizeHC(in.HealthCheck, tg.HealthCheck); err != nil {
		return nil, err
	}
	if err := s.Repo.UpdateTG(ctx, tg); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, tg.TargetGroupID, "target_group", "modified", "Target group "+tg.Name+" modified", in)
	return s.view(ctx, tg)
}

func (s *ELBService) DeleteTargetGroup(ctx context.Context, account, id string) error {
	tg, err := s.getTG(ctx, account, id)
	if err != nil {
		return err
	}
	if n, err := s.Repo.TGListenerCount(ctx, tg.TargetGroupID); err != nil {
		return err
	} else if n > 0 {
		return apierr.New(409, "ResourceInUse", "target group %s is the default action of %d listener(s); delete them first", tg.Name, n)
	}
	if n, _ := s.Repo.TGASGCount(ctx, tg.TargetGroupID); n > 0 {
		return apierr.New(409, "ResourceInUse", "target group %s is attached to %d Auto Scaling group(s)", tg.Name, n)
	}
	if err := s.Repo.DeleteTG(ctx, tg.TargetGroupID); err != nil {
		return RepoErr(err, "target group "+id)
	}
	_ = s.Engine.Deregister(ctx, tg.TargetGroupID)
	s.RecordEvent(ctx, account, tg.TargetGroupID, "target_group", "deleted", "Target group "+tg.Name+" deleted", nil)
	return nil
}

type TargetInput struct {
	ID     string `json:"id"`
	Port   int    `json:"port"`
	Weight *int   `json:"weight"`
}

func (s *ELBService) RegisterTargets(ctx context.Context, account, tgID string, targets []TargetInput) (*TargetGroupView, error) {
	tg, err := s.getTG(ctx, account, tgID)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 || len(targets) > 100 {
		return nil, apierr.Validation("targets must contain 1-100 entries")
	}
	for _, t := range targets {
		inst, err := s.Store.EC2.GetInstance(ctx, account, t.ID)
		if err != nil {
			return nil, apierr.New(400, "InvalidTarget", "instance %q does not exist", t.ID)
		}
		if inst.State == "terminated" {
			return nil, apierr.New(400, "InvalidTarget", "instance %s is terminated", t.ID)
		}
		if inst.VPCID != tg.VPCID {
			return nil, apierr.New(400, "InvalidTarget", "instance %s is in %s but target group %s is in %s", t.ID, inst.VPCID, tg.Name, tg.VPCID)
		}
		if t.Port == 0 {
			t.Port = tg.Port
		}
		if t.Port < 1 || t.Port > 65535 {
			return nil, apierr.Validation("target port must be between 1 and 65535")
		}
		if t.Weight != nil && (*t.Weight < 0 || *t.Weight > 999) {
			return nil, apierr.Validation("weight must be between 0 and 999")
		}
	}
	for _, t := range targets {
		if t.Port == 0 {
			t.Port = tg.Port
		}
		w := 1
		if t.Weight != nil {
			w = *t.Weight
		}
		if err := s.Repo.UpsertTarget(ctx, &repositories.ELBTarget{TargetGroupID: tg.TargetGroupID, InstanceID: t.ID, Port: t.Port, Weight: w}); err != nil {
			return nil, err
		}
		s.RecordEvent(ctx, account, tg.TargetGroupID, "target_group", "target_registered", fmt.Sprintf("Target %s:%d registered with %s", t.ID, t.Port, tg.Name), nil)
	}
	return s.view(ctx, tg)
}

// RegisterInstance registers one instance on the target group's port (used by Auto Scaling).
func (s *ELBService) RegisterInstance(ctx context.Context, account, tgID, instanceID string) error {
	_, err := s.RegisterTargets(ctx, account, tgID, []TargetInput{{ID: instanceID}})
	return err
}

func (s *ELBService) DeregisterTarget(ctx context.Context, account, tgID, instanceID string) error {
	tg, err := s.getTG(ctx, account, tgID)
	if err != nil {
		return err
	}
	ok, err := s.Repo.DeleteTarget(ctx, tg.TargetGroupID, instanceID)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("target %s is not registered with %s", instanceID, tg.Name)
	}
	s.RecordEvent(ctx, account, tg.TargetGroupID, "target_group", "target_deregistered", fmt.Sprintf("Target %s deregistered from %s", instanceID, tg.Name), nil)
	return nil
}

// OnInstanceTerminated removes a terminated instance from every target group.
func (s *ELBService) OnInstanceTerminated(ctx context.Context, account, instanceID string) {
	tgs, err := s.Repo.TargetGroupsOfInstance(ctx, instanceID)
	if err != nil || len(tgs) == 0 {
		return
	}
	if _, err := s.Repo.DeleteTargetsOfInstance(ctx, instanceID); err != nil {
		return
	}
	for _, tg := range tgs {
		s.RecordEvent(ctx, account, tg, "target_group", "target_deregistered", fmt.Sprintf("Target %s deregistered from %s (instance terminated)", instanceID, tg), nil)
	}
}

// ---- Health

func ruleAllowsPort(r repositories.SGRule, port int) bool {
	p := strings.ToLower(r.Protocol)
	if p == "-1" || p == "all" {
		return true
	}
	return p == "tcp" && r.FromPort <= port && port <= r.ToPort
}

func cidrCovers(cidr string, target netip.Prefix) bool {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return false
	}
	return p.Bits() <= target.Bits() && p.Contains(target.Addr())
}

// sgPathAllowed checks that the LB's security groups allow egress to the instance and the instance's
// security groups allow ingress from the LB (by SG reference or a CIDR covering an LB subnet).
func (s *ELBService) sgPathAllowed(ctx context.Context, lb *repositories.LoadBalancer, inst *repositories.Instance, port int) (bool, string) {
	lbSG := map[string]bool{}
	for _, g := range lb.SecurityGroupIDs {
		lbSG[g] = true
	}
	instSG := map[string]bool{}
	for _, g := range inst.SecurityGroupIDs {
		instSG[g] = true
	}
	var lbNets []netip.Prefix
	for _, sid := range lb.SubnetIDs {
		if sn, err := s.Store.Network.GetSubnet(ctx, lb.AccountID, sid); err == nil {
			if p, err := netip.ParsePrefix(sn.CIDRBlock); err == nil {
				lbNets = append(lbNets, p)
			}
		}
	}
	instIP, _ := netip.ParseAddr(inst.PrivateIP)
	egress := false
	for _, g := range lb.SecurityGroupIDs {
		sg, err := s.Store.Network.GetSG(ctx, lb.AccountID, g)
		if err != nil {
			continue
		}
		for _, r := range sg.Egress {
			if !ruleAllowsPort(r, port) {
				continue
			}
			if (r.SourceSecurityGroupID != "" && instSG[r.SourceSecurityGroupID]) || (r.CIDR != "" && instIP.IsValid() && cidrCovers(r.CIDR, netip.PrefixFrom(instIP, 32))) {
				egress = true
			}
		}
	}
	if !egress {
		return false, fmt.Sprintf("Target.Timeout: security groups of load balancer %s do not allow outbound traffic to %s on port %d", lb.Name, inst.InstanceID, port)
	}
	for _, g := range inst.SecurityGroupIDs {
		sg, err := s.Store.Network.GetSG(ctx, lb.AccountID, g)
		if err != nil {
			continue
		}
		for _, r := range sg.Ingress {
			if !ruleAllowsPort(r, port) {
				continue
			}
			if r.SourceSecurityGroupID != "" && lbSG[r.SourceSecurityGroupID] {
				return true, ""
			}
			for _, n := range lbNets {
				if r.CIDR != "" && cidrCovers(r.CIDR, n) {
					return true, ""
				}
			}
		}
	}
	return false, fmt.Sprintf("Target.Timeout: security groups of %s (%s) do not allow inbound TCP %d from load balancer %s", inst.InstanceID, strings.Join(inst.SecurityGroupIDs, ", "), port, lb.Name)
}

// TargetHealth combines engine health with simulator state: the instance must be running, a service
// must listen on the target port (sandboxed user-data result) and security groups must allow the LB.
func (s *ELBService) TargetHealth(ctx context.Context, tg *repositories.TargetGroup) ([]TargetStatus, error) {
	targets, err := s.Repo.ListTargets(ctx, tg.TargetGroupID)
	if err != nil {
		return nil, err
	}
	out := make([]TargetStatus, 0, len(targets))
	if len(targets) == 0 {
		return out, nil
	}
	lbIDs, err := s.Repo.LBsOfTG(ctx, tg.TargetGroupID)
	if err != nil {
		return nil, err
	}
	var lbs []*repositories.LoadBalancer
	for _, id := range lbIDs {
		if lb, err := s.Repo.GetLBAny(ctx, id); err == nil {
			lbs = append(lbs, lb)
		}
	}
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.InstanceID)
	}
	insts, err := s.Store.EC2.ListInstances(ctx, repositories.InstanceFilter{AccountID: tg.AccountID, IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := map[string]*repositories.Instance{}
	for i := range insts {
		byID[insts[i].InstanceID] = &insts[i]
	}
	engine := map[string][2]string{}
	if len(lbs) > 0 {
		req := map[string]any{"healthy_threshold": tg.HealthCheck.HealthyThreshold, "unhealthy_threshold": tg.HealthCheck.UnhealthyThreshold}
		var ts []map[string]any
		for _, t := range targets {
			if i := byID[t.InstanceID]; i != nil && i.State == "running" {
				ts = append(ts, map[string]any{"id": t.InstanceID, "port": t.Port})
			}
		}
		if len(ts) > 0 {
			req["targets"] = ts
			var resp struct {
				Targets []struct {
					ID     string `json:"id"`
					State  string `json:"state"`
					Reason string `json:"reason"`
				} `json:"targets"`
			}
			if err := s.Engine.Raw(ctx, "POST", "/v1/lb/health", req, &resp); err == nil {
				for _, r := range resp.Targets {
					engine[r.ID] = [2]string{r.State, r.Reason}
				}
			} else {
				s.Log.Warn("engine lb health unavailable; using backend state only", "err", err)
			}
		}
	}
	for _, t := range targets {
		st := TargetStatus{ID: t.InstanceID, Port: t.Port, Weight: t.Weight}
		inst := byID[t.InstanceID]
		switch {
		case inst == nil:
			st.Health, st.Reason = "unhealthy", "Target.NotRegistered: instance not found"
		case len(lbs) == 0:
			st.Health, st.Reason = "unused", "Target.NotInUse: target group is not used by any load balancer listener"
		case inst.State == "pending":
			st.Health, st.Reason = "initial", "Elb.RegistrationInProgress: instance is starting"
		case inst.State != "running":
			st.Health, st.Reason = "unused", "Target.InvalidState: instance is "+inst.State
		case engine[t.InstanceID][0] != "" && engine[t.InstanceID][0] != "healthy":
			st.Health, st.Reason = "unhealthy", engine[t.InstanceID][1]
			if st.Reason == "" {
				st.Reason = "Target.FailedHealthChecks"
			}
		default:
			ports := s.EC2.ListeningPorts(inst)
			if _, ok := ports[t.Port]; !ok {
				var svcs []string
				for p, n := range ports {
					svcs = append(svcs, fmt.Sprintf("%s:%d", n, p))
				}
				sort.Strings(svcs)
				what := fmt.Sprintf("health check %s %s on port %d failed", tg.Protocol, tg.HealthCheck.Path, t.Port)
				if tg.Protocol == "TCP" {
					what = fmt.Sprintf("TCP connection to port %d refused", t.Port)
				}
				st.Health, st.Reason = "unhealthy", fmt.Sprintf("Target.FailedHealthChecks: %s — no service is listening (running: %s)", what, strings.Join(svcs, ", "))
				break
			}
			ok, why := false, ""
			for _, lb := range lbs {
				if ok, why = s.sgPathAllowed(ctx, lb, inst, t.Port); ok {
					break
				}
			}
			if !ok {
				st.Health, st.Reason = "unhealthy", why
			} else {
				st.Health = "healthy"
			}
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *ELBService) TargetGroupHealth(ctx context.Context, account, tgID string) (map[string]any, error) {
	tg, err := s.getTG(ctx, account, tgID)
	if err != nil {
		return nil, err
	}
	h, err := s.TargetHealth(ctx, tg)
	if err != nil {
		return nil, err
	}
	return map[string]any{"target_group_id": tg.TargetGroupID, "targets": h}, nil
}

// InstanceHealthy reports ELB health of an instance across target groups (used by Auto Scaling).
func (s *ELBService) InstanceHealthy(ctx context.Context, account string, tgIDs []string, instanceID string) (bool, string) {
	for _, id := range tgIDs {
		tg, err := s.Repo.GetTG(ctx, account, id)
		if err != nil {
			continue
		}
		h, err := s.TargetHealth(ctx, tg)
		if err != nil {
			continue
		}
		for _, t := range h {
			if t.ID == instanceID && t.Health == "unhealthy" {
				return false, t.Reason
			}
		}
	}
	return true, ""
}

// ---- Load balancers

type CreateLoadBalancerInput struct {
	Name             string   `json:"name"`
	Scheme           string   `json:"scheme"`
	SubnetIDs        []string `json:"subnet_ids"`
	SecurityGroupIDs []string `json:"security_group_ids"`
}

func lbDNS(name, id string) string {
	h := sha1.Sum([]byte(id))
	return strings.ToLower(name) + "-" + hex.EncodeToString(h[:])[:10] + ".elb.cloudsim.local"
}

func (s *ELBService) CreateLoadBalancer(ctx context.Context, account string, in CreateLoadBalancerInput) (*LoadBalancerView, error) {
	if !elbNameRe.MatchString(in.Name) || strings.HasPrefix(strings.ToLower(in.Name), "internal-") {
		return nil, apierr.Validation("name must be 1-32 alphanumeric characters or hyphens, not starting/ending with a hyphen or 'internal-'")
	}
	if in.Scheme == "" {
		in.Scheme = "internet-facing"
	}
	if !lbSchemes[in.Scheme] {
		return nil, apierr.Validation("scheme must be internet-facing or internal")
	}
	if len(in.SubnetIDs) == 0 {
		return nil, apierr.Validation("at least one subnet is required (two in different availability zones recommended)")
	}
	vpcID := ""
	azs := map[string]string{}
	seen := map[string]bool{}
	var subnets []string
	for _, sid := range in.SubnetIDs {
		if seen[sid] {
			continue
		}
		seen[sid] = true
		sn, err := s.Net.GetSubnet(ctx, account, sid)
		if err != nil {
			return nil, err
		}
		if vpcID == "" {
			vpcID = sn.VPCID
		} else if sn.VPCID != vpcID {
			return nil, apierr.Validation("all subnets must belong to the same VPC (%s is in %s, expected %s)", sid, sn.VPCID, vpcID)
		}
		if other, ok := azs[sn.AvailabilityZone]; ok {
			return nil, apierr.Validation("subnets %s and %s are both in %s; specify at most one subnet per availability zone", other, sid, sn.AvailabilityZone)
		}
		azs[sn.AvailabilityZone] = sid
		subnets = append(subnets, sid)
	}
	if in.Scheme == "internet-facing" {
		igws, err := s.Store.Network.ListIGWs(ctx, account)
		if err != nil {
			return nil, err
		}
		ok := false
		for _, g := range igws {
			if g.VPCID != nil && *g.VPCID == vpcID {
				ok = true
			}
		}
		if !ok {
			return nil, apierr.New(400, "InvalidSubnet", "VPC %s has no internet gateway attached; an internet-facing load balancer requires one", vpcID)
		}
	}
	sgs, err := s.Net.ResolveSGs(ctx, account, vpcID, in.SecurityGroupIDs)
	if err != nil {
		return nil, err
	}
	id := ids.New("lb")
	lb := &repositories.LoadBalancer{LoadBalancerID: id, AccountID: account, Name: in.Name, DNSName: lbDNS(in.Name, id), Scheme: in.Scheme,
		State: "provisioning", VPCID: vpcID, SubnetIDs: subnets, SecurityGroupIDs: sgs}
	if err := s.Repo.InsertLB(ctx, lb); err != nil {
		return nil, RepoErr(err, "load balancer "+in.Name)
	}
	res, err := s.Engine.Register(ctx, id, simulator.RegisterRequest{Kind: "load_balancer", Owner: account, DependsOn: subnets,
		Attributes: map[string]any{"scheme": in.Scheme, "dns_name": lb.DNSName}})
	if err != nil {
		_ = s.Repo.DeleteLB(ctx, id)
		return nil, EngineErr(err)
	}
	s.RecordEvent(ctx, account, id, "load_balancer", "created", fmt.Sprintf("Load balancer %s (%s) created in %s", in.Name, in.Scheme, vpcID), nil)
	if res.State != "" && res.State != lb.State {
		s.OnLBState(ctx, id, lb.State, res.State, "")
	}
	return s.GetLoadBalancer(ctx, account, id)
}

func (s *ELBService) lbView(ctx context.Context, lb *repositories.LoadBalancer) (*LoadBalancerView, error) {
	ls, err := s.Repo.ListListeners(ctx, lb.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	return &LoadBalancerView{LoadBalancer: *lb, ARN: s.lbARN(lb.AccountID, lb.LoadBalancerID), Listeners: ls}, nil
}

func (s *ELBService) getLB(ctx context.Context, account, id string) (*repositories.LoadBalancer, error) {
	lb, err := s.Repo.GetLB(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "load balancer "+id)
	}
	return lb, nil
}

func (s *ELBService) GetLoadBalancer(ctx context.Context, account, id string) (*LoadBalancerView, error) {
	lb, err := s.getLB(ctx, account, id)
	if err != nil {
		return nil, err
	}
	return s.lbView(ctx, lb)
}

func (s *ELBService) ListLoadBalancers(ctx context.Context, account string) ([]LoadBalancerView, error) {
	l, err := s.Repo.ListLBs(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]LoadBalancerView, 0, len(l))
	for i := range l {
		v, err := s.lbView(ctx, &l[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// OnLBState applies an engine lifecycle change.
func (s *ELBService) OnLBState(ctx context.Context, id, from, to, reason string) {
	lb, err := s.Repo.GetLBAny(ctx, id)
	if err != nil {
		return
	}
	if to == "deleted" {
		s.finalizeLB(ctx, lb)
		return
	}
	if lb.State == to {
		return
	}
	if err := s.Repo.SetLBState(ctx, id, to); err != nil {
		s.Log.Error("update load balancer state", "id", id, "err", err)
		return
	}
	s.RecordEvent(ctx, lb.AccountID, id, "load_balancer", "state_changed", fmt.Sprintf("%s: %s → %s", lb.Name, lb.State, to), map[string]string{"from": lb.State, "to": to, "reason": reason})
}

func (s *ELBService) finalizeLB(ctx context.Context, lb *repositories.LoadBalancer) {
	_ = s.Engine.Deregister(ctx, lb.LoadBalancerID)
	if err := s.Repo.DeleteLB(ctx, lb.LoadBalancerID); err != nil {
		s.Log.Error("delete load balancer", "id", lb.LoadBalancerID, "err", err)
		return
	}
	s.RecordEvent(ctx, lb.AccountID, lb.LoadBalancerID, "load_balancer", "deleted", "Load balancer "+lb.Name+" deleted", nil)
}

func (s *ELBService) DeleteLoadBalancer(ctx context.Context, account, id string) error {
	lb, err := s.getLB(ctx, account, id)
	if err != nil {
		return err
	}
	if lb.State == "deleting" {
		return nil
	}
	res, err := s.Engine.Action(ctx, lb.LoadBalancerID, "delete", "User initiated")
	if err != nil {
		if simulator.IsCode(err, "not_found") {
			s.finalizeLB(ctx, lb)
			return nil
		}
		return EngineErr(err)
	}
	if res.State == "deleted" {
		s.finalizeLB(ctx, lb)
		return nil
	}
	s.OnLBState(ctx, lb.LoadBalancerID, lb.State, "deleting", "User initiated")
	return nil
}

type CreateListenerInput struct {
	Protocol             string `json:"protocol"`
	Port                 int    `json:"port"`
	DefaultTargetGroupID string `json:"default_target_group_id"`
}

func (s *ELBService) CreateListener(ctx context.Context, account, lbID string, in CreateListenerInput) (*repositories.Listener, error) {
	lb, err := s.getLB(ctx, account, lbID)
	if err != nil {
		return nil, err
	}
	if lb.State == "deleting" {
		return nil, apierr.InvalidState("load balancer %s is being deleted", lb.Name)
	}
	in.Protocol = strings.ToUpper(in.Protocol)
	if in.Protocol == "" {
		in.Protocol = "HTTP"
	}
	if !elbProtos[in.Protocol] {
		return nil, apierr.Validation("protocol must be HTTP, HTTPS or TCP")
	}
	if in.Port == 0 {
		in.Port = defaultPort[in.Protocol]
	}
	if in.Port < 1 || in.Port > 65535 {
		return nil, apierr.Validation("port must be between 1 and 65535")
	}
	if in.DefaultTargetGroupID == "" {
		return nil, apierr.Validation("default_target_group_id is required")
	}
	tg, err := s.getTG(ctx, account, in.DefaultTargetGroupID)
	if err != nil {
		return nil, err
	}
	if tg.VPCID != lb.VPCID {
		return nil, apierr.Validation("target group %s is in %s but load balancer %s is in %s", tg.Name, tg.VPCID, lb.Name, lb.VPCID)
	}
	if (in.Protocol == "TCP") != (tg.Protocol == "TCP") {
		return nil, apierr.Validation("listener protocol %s is incompatible with target group protocol %s", in.Protocol, tg.Protocol)
	}
	existing, err := s.Repo.ListListeners(ctx, lb.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	if len(existing) >= 50 {
		return nil, apierr.Quota("a load balancer can have at most 50 listeners")
	}
	for _, l := range existing {
		if l.Port == in.Port {
			return nil, apierr.New(409, "DuplicateListener", "load balancer %s already has a listener on port %d", lb.Name, in.Port)
		}
	}
	l := &repositories.Listener{ListenerID: ids.New("lst"), LoadBalancerID: lb.LoadBalancerID, Protocol: in.Protocol, Port: in.Port, DefaultTargetGroupID: tg.TargetGroupID}
	if err := s.Repo.InsertListener(ctx, l); err != nil {
		return nil, RepoErr(err, fmt.Sprintf("listener on port %d", in.Port))
	}
	s.RecordEvent(ctx, account, lb.LoadBalancerID, "load_balancer", "listener_created", fmt.Sprintf("Listener %s:%d → %s added to %s", in.Protocol, in.Port, tg.Name, lb.Name), nil)
	return l, nil
}

func (s *ELBService) DeleteListener(ctx context.Context, account, lbID, listenerID string) error {
	lb, err := s.getLB(ctx, account, lbID)
	if err != nil {
		return err
	}
	ok, err := s.Repo.DeleteListener(ctx, lb.LoadBalancerID, listenerID)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("listener %s not found on %s", listenerID, lb.Name)
	}
	s.RecordEvent(ctx, account, lb.LoadBalancerID, "load_balancer", "listener_deleted", "Listener "+listenerID+" removed from "+lb.Name, nil)
	return nil
}

type SimulateTrafficInput struct {
	Requests     int `json:"requests"`
	ListenerPort int `json:"listener_port"`
	DurationS    int `json:"duration_s"`
}

// SimulateTraffic sends healthy targets of the listener's target group to the engine's LB model
// with apply_load, so target CPU rises accordingly.
func (s *ELBService) SimulateTraffic(ctx context.Context, account, lbID string, in SimulateTrafficInput) (map[string]any, error) {
	lb, err := s.getLB(ctx, account, lbID)
	if err != nil {
		return nil, err
	}
	if lb.State != "active" {
		return nil, apierr.InvalidState("load balancer %s is %s; it must be active", lb.Name, lb.State)
	}
	if in.Requests == 0 {
		in.Requests = 1000
	}
	if in.Requests < 1 || in.Requests > 1_000_000 {
		return nil, apierr.Validation("requests must be between 1 and 1000000")
	}
	if in.DurationS == 0 {
		in.DurationS = 60
	}
	if in.DurationS < 1 || in.DurationS > 3600 {
		return nil, apierr.Validation("duration_s must be between 1 and 3600")
	}
	ls, err := s.Repo.ListListeners(ctx, lb.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	var lst *repositories.Listener
	for i := range ls {
		if in.ListenerPort == 0 || ls[i].Port == in.ListenerPort {
			lst = &ls[i]
			break
		}
	}
	if lst == nil {
		if in.ListenerPort != 0 {
			return nil, apierr.Validation("load balancer %s has no listener on port %d", lb.Name, in.ListenerPort)
		}
		return nil, apierr.Validation("load balancer %s has no listeners; add one first", lb.Name)
	}
	tg, err := s.Repo.GetTG(ctx, account, lst.DefaultTargetGroupID)
	if err != nil {
		return nil, RepoErr(err, "target group")
	}
	health, err := s.TargetHealth(ctx, tg)
	if err != nil {
		return nil, err
	}
	targets := []map[string]any{}
	for _, t := range health {
		capacity := 100
		if inst, err := s.Store.EC2.GetInstance(ctx, account, t.ID); err == nil {
			capacity = inst.VCPU * 50
		}
		targets = append(targets, map[string]any{"id": t.ID, "healthy": t.Health == "healthy", "weight": max(t.Weight, 0), "capacity_rps": capacity})
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(lb.LoadBalancerID))
	var out map[string]any
	if err := s.Engine.Raw(ctx, "POST", "/v1/lb/simulate", map[string]any{"algorithm": tg.Algorithm, "requests": in.Requests, "targets": targets,
		"seed": int(h.Sum32() % 100000), "apply_load": true, "duration_s": in.DurationS}, &out); err != nil {
		return nil, EngineErr(err)
	}
	if out == nil {
		out = map[string]any{}
	}
	out["load_balancer_id"], out["listener_port"], out["target_group_id"], out["algorithm"], out["targets"] = lb.LoadBalancerID, lst.Port, tg.TargetGroupID, tg.Algorithm, health
	s.RecordEvent(ctx, account, lb.LoadBalancerID, "load_balancer", "traffic_simulated",
		fmt.Sprintf("Simulated %d requests over %ds through %s:%d → %s", in.Requests, in.DurationS, lb.Name, lst.Port, tg.Name), map[string]any{"served": out["served"], "dropped": out["dropped"]})
	return out, nil
}

// ---- Topology / DNS / resync

func (s *ELBService) Topology(ctx context.Context, account, vpcID string) []map[string]any {
	lbs, err := s.Repo.ListLBs(ctx, account)
	if err != nil {
		return nil
	}
	out := []map[string]any{}
	for _, lb := range lbs {
		if lb.VPCID != vpcID {
			continue
		}
		targets := []string{}
		ls, _ := s.Repo.ListListeners(ctx, lb.LoadBalancerID)
		seen := map[string]bool{}
		for _, l := range ls {
			ts, _ := s.Repo.ListTargets(ctx, l.DefaultTargetGroupID)
			for _, t := range ts {
				if !seen[t.InstanceID] {
					seen[t.InstanceID] = true
					targets = append(targets, t.InstanceID)
				}
			}
		}
		out = append(out, map[string]any{"load_balancer_id": lb.LoadBalancerID, "name": lb.Name, "dns_name": lb.DNSName, "scheme": lb.Scheme,
			"state": lb.State, "subnet_ids": lb.SubnetIDs, "security_group_ids": lb.SecurityGroupIDs, "listeners": ls, "target_instance_ids": targets})
	}
	return out
}

// ResolveDNS answers queries for load balancer DNS names (one address per LB subnet).
func (s *ELBService) ResolveDNS(ctx context.Context, account, name, vpcID string) ([]map[string]any, string) {
	if !strings.HasSuffix(name, ".elb.cloudsim.local") {
		return nil, ""
	}
	lbs, err := s.Repo.ListLBs(ctx, account)
	if err != nil {
		return nil, ""
	}
	for _, lb := range lbs {
		if !strings.EqualFold(lb.DNSName, name) || lb.State != "active" {
			continue
		}
		var answers []map[string]any
		for _, sid := range lb.SubnetIDs {
			h := sha1.Sum([]byte(lb.LoadBalancerID + sid))
			ip := ""
			if lb.Scheme == "internet-facing" && (vpcID == "" || vpcID != lb.VPCID) {
				ip = fmt.Sprintf("3.%d.%d.%d", h[0], h[1], max(h[2], 1))
			} else if sn, err := s.Store.Network.GetSubnet(ctx, account, sid); err == nil {
				if p, err := netip.ParsePrefix(sn.CIDRBlock); err == nil {
					ip = intToAddr(addrToInt(p.Masked().Addr()) + uint32(prefixSize(p)) - 2).String()
				}
			}
			if ip != "" {
				answers = append(answers, map[string]any{"type": "A", "value": ip, "ttl": 60})
			}
		}
		return answers, "elb"
	}
	return nil, ""
}

// Resync re-registers target groups and load balancers with the engine.
func (s *ELBService) Resync(ctx context.Context) error {
	tgs, err := s.Repo.ListAllTGs(ctx)
	if err != nil {
		return err
	}
	for _, tg := range tgs {
		if _, err := s.Engine.Register(ctx, tg.TargetGroupID, simulator.RegisterRequest{Kind: "target_group", Owner: tg.AccountID, State: strPtr("available"), DependsOn: []string{tg.VPCID}}); err != nil {
			return err
		}
	}
	lbs, err := s.Repo.ListAllLBs(ctx)
	if err != nil {
		return err
	}
	for _, lb := range lbs {
		res, err := s.Engine.Register(ctx, lb.LoadBalancerID, simulator.RegisterRequest{Kind: "load_balancer", Owner: lb.AccountID, State: strPtr(lb.State), DependsOn: lb.SubnetIDs,
			Attributes: map[string]any{"scheme": lb.Scheme, "dns_name": lb.DNSName}})
		if err != nil {
			return err
		}
		if res.State != "" && res.State != lb.State {
			s.OnLBState(ctx, lb.LoadBalancerID, lb.State, res.State, "resynchronized")
		}
	}
	return nil
}

// CountLoadBalancers returns non-deleted load balancers of an account.
func (s *ELBService) CountLoadBalancers(ctx context.Context, account string) int {
	l, _ := s.Repo.ListLBs(ctx, account)
	return len(l)
}

package services

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

// AutoScalingService implements launch templates, Auto Scaling groups, policies and the controller.
type AutoScalingService struct {
	*Deps
	EC2  *EC2Service
	Net  *NetworkService
	ELB  *ELBService
	CW   *CloudWatchService
	Repo repositories.AutoScalingRepo

	// CPUWindow is how far back CloudWatch CPU samples are averaged for scaling decisions.
	CPUWindow time.Duration
	mu        sync.Mutex
}

func NewAutoScalingService(d *Deps, ec2 *EC2Service, n *NetworkService, elb *ELBService, cw *CloudWatchService) *AutoScalingService {
	return &AutoScalingService{Deps: d, EC2: ec2, Net: n, ELB: elb, CW: cw, Repo: repositories.NewAutoScalingRepo(d.Store.DB), CPUWindow: 2 * time.Minute}
}

const maxASGSize = 20

var (
	ltNameRe  = regexp.MustCompile(`^[A-Za-z0-9()./_-]{3,128}$`)
	asgNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,255}$`)
	stepCmps  = map[string]bool{">=": true, ">": true, "<=": true, "<": true}
)

func (s *AutoScalingService) groupARN(account, name string) string {
	return auth.ARN("autoscaling", account, "autoScalingGroup/"+name)
}

// PolicyARN is the ARN used by CloudWatch alarm actions.
func (s *AutoScalingService) PolicyARN(account, group, policy string) string {
	return auth.ARN("autoscaling", account, "policy/"+group+"/"+policy)
}

// ---- Launch templates

type CreateLaunchTemplateInput struct {
	Name             string   `json:"name"`
	ImageID          string   `json:"image_id"`
	InstanceType     string   `json:"instance_type"`
	SecurityGroupIDs []string `json:"security_group_ids"`
	KeyName          string   `json:"key_name"`
	UserData         string   `json:"user_data"`
	WorkloadProfile  string   `json:"workload_profile"`
}

func (s *AutoScalingService) CreateLaunchTemplate(ctx context.Context, account string, in CreateLaunchTemplateInput) (*repositories.LaunchTemplate, error) {
	if !ltNameRe.MatchString(in.Name) {
		return nil, apierr.Validation("name must be 3-128 characters of letters, digits, ( ) . / _ -")
	}
	img, ok := FindImage(in.ImageID)
	if !ok {
		return nil, apierr.New(400, "InvalidAMIID.NotFound", "image %q does not exist; see GET /ec2/images", in.ImageID)
	}
	if _, ok := findType(in.InstanceType); !ok {
		return nil, apierr.Validation("unknown instance type %q; see GET /ec2/instance-types", in.InstanceType)
	}
	if in.KeyName != "" {
		if _, err := s.Store.EC2.GetKeyPair(ctx, account, in.KeyName); err != nil {
			return nil, apierr.New(400, "InvalidKeyPair.NotFound", "key pair %q does not exist", in.KeyName)
		}
	}
	if len(in.UserData) > 16*1024 {
		return nil, apierr.Validation("user_data is limited to 16 KiB")
	}
	if in.WorkloadProfile == "" {
		in.WorkloadProfile = "steady"
	}
	if !workloadProfiles[in.WorkloadProfile] || in.WorkloadProfile == "custom" {
		return nil, apierr.Validation("workload_profile must be idle, steady, spiky or stress")
	}
	if len(in.SecurityGroupIDs) > 5 {
		return nil, apierr.Validation("at most 5 security groups")
	}
	vpc := ""
	for _, g := range in.SecurityGroupIDs {
		sg, err := s.Net.GetSG(ctx, account, g)
		if err != nil {
			return nil, err
		}
		if vpc != "" && sg.VPCID != vpc {
			return nil, apierr.Validation("all security groups must belong to the same VPC")
		}
		vpc = sg.VPCID
	}
	if in.SecurityGroupIDs == nil {
		in.SecurityGroupIDs = []string{}
	}
	t := &repositories.LaunchTemplate{LaunchTemplateID: ids.New("lt"), AccountID: account, Name: in.Name, ImageID: img.ImageID, InstanceType: in.InstanceType,
		SecurityGroupIDs: in.SecurityGroupIDs, KeyName: in.KeyName, UserData: in.UserData, WorkloadProfile: in.WorkloadProfile}
	if err := s.Repo.InsertTemplate(ctx, t); err != nil {
		return nil, RepoErr(err, "launch template "+in.Name)
	}
	s.RecordEvent(ctx, account, t.LaunchTemplateID, "launch_template", "created", "Launch template "+in.Name+" created", nil)
	return t, nil
}

func (s *AutoScalingService) ListLaunchTemplates(ctx context.Context, account string) ([]repositories.LaunchTemplate, error) {
	return s.Repo.ListTemplates(ctx, account)
}

func (s *AutoScalingService) GetLaunchTemplate(ctx context.Context, account, name string) (*repositories.LaunchTemplate, error) {
	t, err := s.Repo.GetTemplate(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "launch template "+name)
	}
	return t, nil
}

func (s *AutoScalingService) DeleteLaunchTemplate(ctx context.Context, account, name string) error {
	t, err := s.GetLaunchTemplate(ctx, account, name)
	if err != nil {
		return err
	}
	if n, err := s.Repo.TemplateInUse(ctx, t.LaunchTemplateID); err != nil {
		return err
	} else if n > 0 {
		return apierr.New(409, "ResourceInUse", "launch template %s is used by %d Auto Scaling group(s)", t.Name, n)
	}
	if err := s.Repo.DeleteTemplate(ctx, t.LaunchTemplateID); err != nil {
		return err
	}
	s.RecordEvent(ctx, account, t.LaunchTemplateID, "launch_template", "deleted", "Launch template "+t.Name+" deleted", nil)
	return nil
}

// ---- Groups

type GroupInstance struct {
	InstanceID string `json:"instance_id"`
	State      string `json:"state"`
	Health     string `json:"health"`
	SubnetID   string `json:"subnet_id"`
	LaunchTime string `json:"launch_time"`
}

type GroupView struct {
	repositories.ASGroup
	ARN       string          `json:"arn"`
	Instances []GroupInstance `json:"instances"`
	Status    string          `json:"status"`
}

type CreateGroupInput struct {
	Name              string   `json:"name"`
	LaunchTemplate    string   `json:"launch_template"`
	MinSize           *int     `json:"min_size"`
	MaxSize           *int     `json:"max_size"`
	DesiredCapacity   *int     `json:"desired_capacity"`
	SubnetIDs         []string `json:"subnet_ids"`
	TargetGroupIDs    []string `json:"target_group_ids"`
	CooldownS         *int     `json:"cooldown_s"`
	HealthCheckType   string   `json:"health_check_type"`
	HealthCheckGraceS *int     `json:"health_check_grace_period_s"`
}

func validateCapacity(min, max, desired int) error {
	if min < 0 || max < 0 || desired < 0 {
		return apierr.Validation("sizes must be non-negative")
	}
	if max > maxASGSize {
		return apierr.Validation("max_size cannot exceed %d in the simulator", maxASGSize)
	}
	if min > max {
		return apierr.Validation("min_size (%d) must be ≤ max_size (%d)", min, max)
	}
	if desired < min || desired > max {
		return apierr.Validation("desired_capacity (%d) must be between min_size (%d) and max_size (%d)", desired, min, max)
	}
	return nil
}

func (s *AutoScalingService) CreateGroup(ctx context.Context, account string, in CreateGroupInput) (*GroupView, error) {
	if !asgNameRe.MatchString(in.Name) {
		return nil, apierr.Validation("name must be 1-255 characters of letters, digits, '.', '_' or '-'")
	}
	if in.MinSize == nil || in.MaxSize == nil {
		return nil, apierr.Validation("min_size and max_size are required")
	}
	desired := *in.MinSize
	if in.DesiredCapacity != nil {
		desired = *in.DesiredCapacity
	}
	if err := validateCapacity(*in.MinSize, *in.MaxSize, desired); err != nil {
		return nil, err
	}
	if in.LaunchTemplate == "" {
		return nil, apierr.Validation("launch_template is required")
	}
	lt, err := s.GetLaunchTemplate(ctx, account, in.LaunchTemplate)
	if err != nil {
		return nil, err
	}
	if len(in.SubnetIDs) == 0 {
		return nil, apierr.Validation("at least one subnet is required")
	}
	vpc := ""
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
		if vpc != "" && sn.VPCID != vpc {
			return nil, apierr.Validation("all subnets must belong to the same VPC")
		}
		vpc = sn.VPCID
		subnets = append(subnets, sid)
	}
	if _, err := s.Net.ResolveSGs(ctx, account, vpc, lt.SecurityGroupIDs); err != nil {
		return nil, apierr.Validation("launch template %s security groups are not usable in %s: %v", lt.Name, vpc, apierr.As(err).Message)
	}
	tgs := []string{}
	for _, t := range in.TargetGroupIDs {
		tg, err := s.ELB.getTG(ctx, account, t)
		if err != nil {
			return nil, err
		}
		if tg.VPCID != vpc {
			return nil, apierr.Validation("target group %s is in %s, but the group's subnets are in %s", tg.Name, tg.VPCID, vpc)
		}
		tgs = append(tgs, tg.TargetGroupID)
	}
	cooldown, grace := 60, 60
	if in.CooldownS != nil {
		cooldown = *in.CooldownS
	}
	if in.HealthCheckGraceS != nil {
		grace = *in.HealthCheckGraceS
	}
	if cooldown < 0 || cooldown > 86400 || grace < 0 || grace > 7200 {
		return nil, apierr.Validation("cooldown_s must be 0-86400 and health_check_grace_period_s 0-7200")
	}
	if in.HealthCheckType == "" {
		in.HealthCheckType = "EC2"
	}
	in.HealthCheckType = strings.ToUpper(in.HealthCheckType)
	if in.HealthCheckType != "EC2" && in.HealthCheckType != "ELB" {
		return nil, apierr.Validation("health_check_type must be EC2 or ELB")
	}
	if in.HealthCheckType == "ELB" && len(tgs) == 0 {
		return nil, apierr.Validation("health_check_type ELB requires at least one target group")
	}
	g := &repositories.ASGroup{GroupID: ids.New("asg"), AccountID: account, Name: in.Name, LaunchTemplateID: lt.LaunchTemplateID, VPCID: vpc,
		MinSize: *in.MinSize, MaxSize: *in.MaxSize, DesiredCapacity: desired, CooldownS: cooldown, HealthCheckType: in.HealthCheckType,
		HealthCheckGraceS: grace, SubnetIDs: subnets, TargetGroupIDs: tgs}
	if err := s.Repo.InsertGroup(ctx, g); err != nil {
		return nil, RepoErr(err, "Auto Scaling group "+in.Name)
	}
	if _, err := s.Engine.Register(ctx, g.GroupID, simulator.RegisterRequest{Kind: "autoscaling_group", Owner: account, DependsOn: subnets,
		Attributes: map[string]any{"name": g.Name}}); err != nil {
		_ = s.Repo.DeleteGroup(ctx, g.GroupID)
		return nil, EngineErr(err)
	}
	s.activity(ctx, g, "Created Auto Scaling group "+g.Name, fmt.Sprintf("User request created group with min %d, max %d, desired %d", g.MinSize, g.MaxSize, g.DesiredCapacity), "Successful")
	s.RecordEvent(ctx, account, g.Name, "autoscaling_group", "created", "Auto Scaling group "+g.Name+" created", nil)
	if _, err := s.Evaluate(ctx, account, g.Name, false); err != nil {
		s.Log.Warn("initial auto scaling reconcile", "group", g.Name, "err", err)
	}
	return s.GetGroup(ctx, account, g.Name)
}

func (s *AutoScalingService) getGroup(ctx context.Context, account, name string) (*repositories.ASGroup, error) {
	g, err := s.Repo.GetGroup(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "Auto Scaling group "+name)
	}
	return g, nil
}

func (s *AutoScalingService) members(ctx context.Context, g *repositories.ASGroup) ([]repositories.Instance, error) {
	l, err := s.Store.EC2.ListInstances(ctx, repositories.InstanceFilter{AccountID: g.AccountID, ASG: g.Name})
	if err != nil {
		return nil, err
	}
	out := l[:0]
	for _, i := range l {
		if i.State != "terminated" {
			out = append(out, i)
		}
	}
	return out, nil
}

func (s *AutoScalingService) groupView(ctx context.Context, g *repositories.ASGroup) (*GroupView, error) {
	insts, err := s.members(ctx, g)
	if err != nil {
		return nil, err
	}
	v := &GroupView{ASGroup: *g, ARN: s.groupARN(g.AccountID, g.Name), Instances: []GroupInstance{}}
	inService := 0
	for _, i := range insts {
		h := "Healthy"
		if i.State != "running" && i.State != "pending" {
			h = "Unhealthy"
		} else if g.HealthCheckType == "ELB" && i.State == "running" && s.pastGrace(g, &i) {
			if ok, _ := s.ELB.InstanceHealthy(ctx, g.AccountID, g.TargetGroupIDs, i.InstanceID); !ok {
				h = "Unhealthy"
			}
		}
		if (i.State == "running" || i.State == "pending") && h == "Healthy" {
			inService++
		}
		v.Instances = append(v.Instances, GroupInstance{InstanceID: i.InstanceID, State: i.State, Health: h, SubnetID: i.SubnetID, LaunchTime: i.LaunchTime})
	}
	v.Status = "InService"
	if inService != g.DesiredCapacity {
		v.Status = "Updating"
	}
	return v, nil
}

func (s *AutoScalingService) GetGroup(ctx context.Context, account, name string) (*GroupView, error) {
	g, err := s.getGroup(ctx, account, name)
	if err != nil {
		return nil, err
	}
	return s.groupView(ctx, g)
}

func (s *AutoScalingService) ListGroups(ctx context.Context, account string) ([]GroupView, error) {
	l, err := s.Repo.ListGroups(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]GroupView, 0, len(l))
	for i := range l {
		v, err := s.groupView(ctx, &l[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

type UpdateGroupInput struct {
	MinSize         *int `json:"min_size"`
	MaxSize         *int `json:"max_size"`
	DesiredCapacity *int `json:"desired_capacity"`
	CooldownS       *int `json:"cooldown_s"`
}

func (s *AutoScalingService) UpdateGroup(ctx context.Context, account, name string, in UpdateGroupInput) (*GroupView, error) {
	g, err := s.getGroup(ctx, account, name)
	if err != nil {
		return nil, err
	}
	min, max, desired := g.MinSize, g.MaxSize, g.DesiredCapacity
	if in.MinSize != nil {
		min = *in.MinSize
	}
	if in.MaxSize != nil {
		max = *in.MaxSize
	}
	if in.DesiredCapacity != nil {
		desired = *in.DesiredCapacity
	} else {
		desired = int(math.Max(float64(min), math.Min(float64(max), float64(desired))))
	}
	if err := validateCapacity(min, max, desired); err != nil {
		return nil, err
	}
	if in.CooldownS != nil {
		if *in.CooldownS < 0 || *in.CooldownS > 86400 {
			return nil, apierr.Validation("cooldown_s must be 0-86400")
		}
		if _, err := s.Store.DB.ExecContext(ctx, `UPDATE asg_groups SET cooldown_s=? WHERE group_id=?`, *in.CooldownS, g.GroupID); err != nil {
			return nil, err
		}
	}
	if err := s.Repo.UpdateCapacity(ctx, g.GroupID, min, max, desired); err != nil {
		return nil, err
	}
	if min != g.MinSize || max != g.MaxSize || desired != g.DesiredCapacity {
		s.activity(ctx, g, fmt.Sprintf("Updating capacity: min %d→%d, max %d→%d, desired %d→%d", g.MinSize, min, g.MaxSize, max, g.DesiredCapacity, desired),
			"User request updated the group's capacity", "Successful")
	}
	if _, err := s.Evaluate(ctx, account, g.Name, false); err != nil {
		s.Log.Warn("auto scaling reconcile", "group", g.Name, "err", err)
	}
	return s.GetGroup(ctx, account, name)
}

func (s *AutoScalingService) DeleteGroup(ctx context.Context, account, name string, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.getGroup(ctx, account, name)
	if err != nil {
		return err
	}
	insts, err := s.members(ctx, g)
	if err != nil {
		return err
	}
	active := 0
	for _, i := range insts {
		if i.State != "shutting-down" {
			active++
		}
	}
	if active > 0 && !force {
		return apierr.New(409, "ResourceInUse", "Auto Scaling group %s still has %d instance(s); set desired capacity to 0 or delete with force=true", g.Name, active)
	}
	for _, i := range insts {
		if i.State == "shutting-down" {
			continue
		}
		if _, err := s.EC2.Action(ctx, account, i.InstanceID, "terminate", "Auto Scaling group "+g.Name+" deleted (force)"); err != nil {
			return err
		}
	}
	if err := s.Repo.DeleteGroup(ctx, g.GroupID); err != nil {
		return RepoErr(err, "Auto Scaling group "+name)
	}
	_ = s.Engine.Deregister(ctx, g.GroupID)
	s.RecordEvent(ctx, account, g.Name, "autoscaling_group", "deleted", fmt.Sprintf("Auto Scaling group %s deleted (%d instance(s) terminated)", g.Name, len(insts)), nil)
	return nil
}

// ---- Policies

type CreatePolicyInput struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	TargetValue *float64 `json:"target_value"`
	Threshold   *float64 `json:"threshold"`
	Comparison  string   `json:"comparison"`
	Adjustment  *int     `json:"adjustment"`
}

func (s *AutoScalingService) CreatePolicy(ctx context.Context, account, group string, in CreatePolicyInput) (*repositories.ScalingPolicy, error) {
	g, err := s.getGroup(ctx, account, group)
	if err != nil {
		return nil, err
	}
	if !asgNameRe.MatchString(in.Name) {
		return nil, apierr.Validation("policy name must be 1-255 characters of letters, digits, '.', '_' or '-'")
	}
	p := &repositories.ScalingPolicy{PolicyID: ids.New("pol"), GroupID: g.GroupID, Name: in.Name, Type: in.Type}
	switch in.Type {
	case "target_tracking":
		if in.TargetValue == nil || *in.TargetValue <= 0 || *in.TargetValue > 100 {
			return nil, apierr.Validation("target_tracking requires target_value (average CPU %%) between 0 and 100")
		}
		p.TargetValue = in.TargetValue
	case "step":
		if in.Threshold == nil || *in.Threshold < 0 || *in.Threshold > 100 {
			return nil, apierr.Validation("step requires threshold (CPU %%) between 0 and 100")
		}
		if in.Comparison == "" {
			in.Comparison = ">="
		}
		if !stepCmps[in.Comparison] {
			return nil, apierr.Validation("comparison must be >=, >, <= or <")
		}
		if in.Adjustment == nil || *in.Adjustment == 0 || *in.Adjustment > maxASGSize || *in.Adjustment < -maxASGSize {
			return nil, apierr.Validation("step requires a non-zero adjustment between -%d and %d", maxASGSize, maxASGSize)
		}
		p.Threshold, p.Comparison, p.Adjustment = in.Threshold, &in.Comparison, in.Adjustment
	default:
		return nil, apierr.Validation("type must be target_tracking or step")
	}
	if err := s.Repo.InsertPolicy(ctx, p); err != nil {
		return nil, RepoErr(err, "scaling policy "+in.Name)
	}
	p.ARN = s.PolicyARN(account, g.Name, p.Name)
	s.RecordEvent(ctx, account, g.Name, "autoscaling_group", "policy_created", fmt.Sprintf("Scaling policy %s (%s) added to %s", p.Name, p.Type, g.Name), nil)
	return p, nil
}

func (s *AutoScalingService) ListPolicies(ctx context.Context, account, group string) ([]repositories.ScalingPolicy, error) {
	g, err := s.getGroup(ctx, account, group)
	if err != nil {
		return nil, err
	}
	l, err := s.Repo.ListPolicies(ctx, g.GroupID)
	for i := range l {
		l[i].ARN = s.PolicyARN(account, g.Name, l[i].Name)
	}
	return l, err
}

func (s *AutoScalingService) DeletePolicy(ctx context.Context, account, group, policy string) error {
	g, err := s.getGroup(ctx, account, group)
	if err != nil {
		return err
	}
	ok, err := s.Repo.DeletePolicy(ctx, g.GroupID, policy)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("scaling policy %s not found in %s", policy, g.Name)
	}
	s.RecordEvent(ctx, account, g.Name, "autoscaling_group", "policy_deleted", "Scaling policy "+policy+" removed from "+g.Name, nil)
	return nil
}

func (s *AutoScalingService) Activities(ctx context.Context, account, group string) ([]repositories.ScalingActivity, error) {
	g, err := s.getGroup(ctx, account, group)
	if err != nil {
		return nil, err
	}
	return s.Repo.ListActivities(ctx, g.GroupID, 200)
}

func (s *AutoScalingService) activity(ctx context.Context, g *repositories.ASGroup, desc, cause, status string) {
	a := &repositories.ScalingActivity{GroupID: g.GroupID, AccountID: g.AccountID, Description: desc, Cause: cause, Status: status}
	if err := s.Repo.InsertActivity(ctx, a); err != nil {
		s.Log.Error("record scaling activity", "err", err)
		return
	}
	s.Pub.Publish(g.AccountID, map[string]any{"type": "autoscaling_activity", "group": g.Name, "activity": a})
}

// parsePolicyARN returns group and policy names from arn:cloudsim:autoscaling:<region>:<account>:policy/<group>/<policy>.
func parsePolicyARN(account, arn string) (string, string, bool) {
	prefix := "arn:cloudsim:autoscaling:"
	if !strings.HasPrefix(arn, prefix) {
		return "", "", false
	}
	_, res, ok := strings.Cut(arn, ":"+account+":policy/")
	if !ok {
		return "", "", false
	}
	g, p, ok := strings.Cut(res, "/")
	return g, p, ok && g != "" && p != ""
}

func (s *AutoScalingService) findPolicy(ctx context.Context, account, arn string) (*repositories.ASGroup, *repositories.ScalingPolicy, error) {
	gname, pname, ok := parsePolicyARN(account, arn)
	if !ok {
		return nil, nil, apierr.Validation("invalid scaling policy ARN %q", arn)
	}
	g, err := s.getGroup(ctx, account, gname)
	if err != nil {
		return nil, nil, err
	}
	ps, err := s.Repo.ListPolicies(ctx, g.GroupID)
	if err != nil {
		return nil, nil, err
	}
	for i := range ps {
		if ps[i].Name == pname {
			return g, &ps[i], nil
		}
	}
	return nil, nil, apierr.NotFound("scaling policy %s not found in %s", pname, gname)
}

// PolicyExists is used by CloudWatch to validate alarm actions.
func (s *AutoScalingService) PolicyExists(ctx context.Context, account, arn string) bool {
	_, _, err := s.findPolicy(ctx, account, arn)
	return err == nil
}

// ExecutePolicyARN runs a scaling policy triggered by a CloudWatch alarm action.
func (s *AutoScalingService) ExecutePolicyARN(ctx context.Context, account, arn, cause string) error {
	g, p, err := s.findPolicy(ctx, account, arn)
	if err != nil {
		return err
	}
	if p.Type == "target_tracking" {
		_, err := s.evaluate(ctx, g, []repositories.ScalingPolicy{*p}, cause+", executing policy "+p.Name)
		return err
	}
	s.mu.Lock()
	g, err = s.getGroup(ctx, account, g.Name)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if s.inCooldown(g) {
		s.mu.Unlock()
		s.activity(ctx, g, "Policy "+p.Name+" not executed: group is within its cooldown period", cause, "Failed")
		return fmt.Errorf("group %s is within its cooldown period", g.Name)
	}
	nd := max(g.MinSize, min(g.MaxSize, g.DesiredCapacity+*p.Adjustment))
	if nd != g.DesiredCapacity {
		if err := s.Repo.SetDesired(ctx, g.GroupID, nd, true); err != nil {
			s.mu.Unlock()
			return err
		}
		s.activity(ctx, g, fmt.Sprintf("Changing desired capacity from %d to %d", g.DesiredCapacity, nd), cause+", executing step policy "+p.Name, "Successful")
		s.RecordEvent(ctx, account, g.Name, "autoscaling_group", "scaling_decision", fmt.Sprintf("%s: desired %d → %d (policy %s)", g.Name, g.DesiredCapacity, nd, p.Name),
			map[string]any{"from": g.DesiredCapacity, "to": nd, "policy": p.Name, "cause": cause})
	}
	s.mu.Unlock()
	_, err = s.Evaluate(ctx, account, g.Name, false)
	return err
}

func (s *AutoScalingService) secondsSinceScaling(g *repositories.ASGroup) float64 {
	if g.LastScalingAt == nil {
		return 1e9
	}
	return time.Since(db.Parse(*g.LastScalingAt)).Seconds()
}

func (s *AutoScalingService) inCooldown(g *repositories.ASGroup) bool {
	return s.secondsSinceScaling(g) < float64(g.CooldownS)
}

func (s *AutoScalingService) pastGrace(g *repositories.ASGroup, i *repositories.Instance) bool {
	return time.Since(db.Parse(i.LaunchTime)) >= time.Duration(g.HealthCheckGraceS)*time.Second
}

// Evaluate runs one controller pass for a group. withPolicies=false only reconciles capacity/health.
func (s *AutoScalingService) Evaluate(ctx context.Context, account, name string, withPolicies bool) (map[string]any, error) {
	g, err := s.getGroup(ctx, account, name)
	if err != nil {
		return nil, err
	}
	var pols []repositories.ScalingPolicy
	if withPolicies {
		if pols, err = s.Repo.ListPolicies(ctx, g.GroupID); err != nil {
			return nil, err
		}
	}
	return s.evaluate(ctx, g, pols, "")
}

// RunController evaluates every group (background worker).
func (s *AutoScalingService) RunController(ctx context.Context) {
	gs, err := s.Repo.ListGroups(ctx, "")
	if err != nil {
		s.Log.Error("list auto scaling groups", "err", err)
		return
	}
	for _, g := range gs {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.Evaluate(ctx, g.AccountID, g.Name, true); err != nil {
			s.Log.Warn("auto scaling controller", "group", g.Name, "err", err)
		}
	}
}

type engineDecision struct {
	Desired int      `json:"desired"`
	Change  int      `json:"change"`
	Reason  string   `json:"reason"`
	Replace []string `json:"replace"`
}

func (s *AutoScalingService) evaluate(ctx context.Context, g0 *repositories.ASGroup, pols []repositories.ScalingPolicy, cause string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.getGroup(ctx, g0.AccountID, g0.Name) // re-read under lock
	if err != nil {
		return nil, err
	}
	insts, err := s.members(ctx, g)
	if err != nil {
		return nil, err
	}
	cpu := s.CW.AverageCPU(ctx, g.AccountID, time.Now().Add(-s.CPUWindow))
	known, sum := 0, 0.0
	for _, i := range insts {
		if v, ok := cpu[i.InstanceID]; ok && i.State == "running" {
			known++
			sum += v
		}
	}
	reasons := map[string]string{}
	var reqInst []map[string]any
	for _, i := range insts {
		if i.State == "shutting-down" {
			continue
		}
		healthy := i.State == "running" || i.State == "pending"
		if !healthy {
			reasons[i.InstanceID] = "instance is " + i.State
		} else if g.HealthCheckType == "ELB" && i.State == "running" && s.pastGrace(g, &i) {
			if ok, why := s.ELB.InstanceHealthy(ctx, g.AccountID, g.TargetGroupIDs, i.InstanceID); !ok {
				healthy = false
				reasons[i.InstanceID] = "ELB health check failed: " + why
			}
		}
		c, ok := cpu[i.InstanceID]
		if !ok && known > 0 {
			c = sum / float64(known)
		}
		reqInst = append(reqInst, map[string]any{"id": i.InstanceID, "state": i.State, "cpu_percent": round(c, 2), "healthy": healthy})
	}
	if known == 0 {
		pols = nil // no CloudWatch data yet: no policy can be evaluated
	}
	ps := []map[string]any{}
	for _, p := range pols {
		m := map[string]any{"name": p.Name, "type": p.Type}
		if p.Type == "target_tracking" {
			m["target_value"] = *p.TargetValue
		} else {
			m["threshold"], m["comparison"], m["adjustment"] = *p.Threshold, *p.Comparison, *p.Adjustment
		}
		ps = append(ps, m)
	}
	if reqInst == nil {
		reqInst = []map[string]any{}
	}
	dec := engineDecision{Desired: g.DesiredCapacity}
	err = s.Engine.Raw(ctx, "POST", "/v1/autoscaling/evaluate", map[string]any{
		"group": map[string]any{"name": g.Name, "min": g.MinSize, "max": g.MaxSize, "desired": g.DesiredCapacity, "cooldown_s": g.CooldownS,
			"seconds_since_last_activity": math.Min(s.secondsSinceScaling(g), 1e9)},
		"policies": ps, "instances": reqInst}, &dec)
	if err != nil {
		s.Log.Warn("engine autoscaling evaluate failed; reconciling capacity only", "group", g.Name, "err", err)
		dec = engineDecision{Desired: g.DesiredCapacity, Reason: "engine unavailable: capacity reconciled only"}
		for id := range reasons {
			dec.Replace = append(dec.Replace, id)
		}
	}
	// Make sure our own health findings are honoured even if the engine did not list them.
	inReplace := map[string]bool{}
	for _, id := range dec.Replace {
		inReplace[id] = true
	}
	for id := range reasons {
		if !inReplace[id] {
			dec.Replace = append(dec.Replace, id)
			inReplace[id] = true
		}
	}
	if cause == "" {
		cause = "Auto Scaling controller evaluation"
	}
	result := map[string]any{"group": g.Name, "previous_desired": g.DesiredCapacity, "desired": g.DesiredCapacity, "change": 0, "reason": dec.Reason,
		"replace": []string{}, "launched": []string{}, "terminated": []string{}}
	// 1. Apply the new desired capacity.
	nd := max(g.MinSize, min(g.MaxSize, dec.Desired))
	if dec.Change != 0 && nd != g.DesiredCapacity {
		if err := s.Repo.SetDesired(ctx, g.GroupID, nd, true); err != nil {
			return nil, err
		}
		s.activity(ctx, g, fmt.Sprintf("Changing desired capacity from %d to %d", g.DesiredCapacity, nd), cause+": "+dec.Reason, "Successful")
		s.RecordEvent(ctx, g.AccountID, g.Name, "autoscaling_group", "scaling_decision", fmt.Sprintf("%s: desired %d → %d (%s)", g.Name, g.DesiredCapacity, nd, dec.Reason),
			map[string]any{"from": g.DesiredCapacity, "to": nd, "reason": dec.Reason})
		result["change"] = nd - g.DesiredCapacity
		g.DesiredCapacity = nd
		result["desired"] = nd
	}
	// 2. Replace unhealthy instances.
	member := map[string]bool{}
	for _, i := range insts {
		member[i.InstanceID] = true
	}
	var replaced, terminated, launched []string
	for _, id := range dec.Replace {
		if !member[id] {
			continue
		}
		why := reasons[id]
		if why == "" {
			why = "instance failed health checks"
		}
		if _, err := s.EC2.Action(ctx, g.AccountID, id, "terminate", "Auto Scaling: replacing unhealthy instance"); err != nil {
			s.activity(ctx, g, "Terminating EC2 instance: "+id, "Failed to terminate unhealthy instance: "+apierr.As(err).Message, "Failed")
			continue
		}
		replaced = append(replaced, id)
		terminated = append(terminated, id)
		s.activity(ctx, g, "Terminating EC2 instance: "+id, "An instance was taken out of service in response to a health check: "+why, "Successful")
	}
	// 3. Reconcile instance count to desired.
	gone := map[string]bool{}
	for _, id := range replaced {
		gone[id] = true
	}
	var active []repositories.Instance // newest first (ListInstances orders by created_at DESC)
	for _, i := range insts {
		if !gone[i.InstanceID] && (i.State == "running" || i.State == "pending") {
			active = append(active, i)
		}
	}
	if len(active) > g.DesiredCapacity {
		for _, i := range active[:len(active)-g.DesiredCapacity] {
			if _, err := s.EC2.Action(ctx, g.AccountID, i.InstanceID, "terminate", "Auto Scaling: scale in"); err != nil {
				s.activity(ctx, g, "Terminating EC2 instance: "+i.InstanceID, "Scale-in failed: "+apierr.As(err).Message, "Failed")
				continue
			}
			terminated = append(terminated, i.InstanceID)
			s.activity(ctx, g, "Terminating EC2 instance: "+i.InstanceID,
				fmt.Sprintf("The desired capacity is %d and the group had %d instances; terminating the newest instance", g.DesiredCapacity, len(active)), "Successful")
		}
	}
	if missing := g.DesiredCapacity - len(active); missing > 0 {
		lt, err := s.Repo.GetTemplateByID(ctx, g.LaunchTemplateID)
		if err != nil {
			return nil, err
		}
		perSubnet := map[string]int{}
		for _, i := range active {
			perSubnet[i.SubnetID]++
		}
		for n := 0; n < missing; n++ {
			subnet := g.SubnetIDs[0]
			for _, sid := range g.SubnetIDs {
				if perSubnet[sid] < perSubnet[subnet] {
					subnet = sid
				}
			}
			out, err := s.EC2.RunInstances(ctx, g.AccountID, RunInstancesInput{ImageID: lt.ImageID, InstanceType: lt.InstanceType, KeyName: lt.KeyName,
				SubnetID: subnet, SecurityGroupIDs: lt.SecurityGroupIDs, UserData: lt.UserData, WorkloadProfile: lt.WorkloadProfile, Name: g.Name,
				Tags: map[string]string{"cloudsim:autoscaling:groupName": g.Name}, AutoScalingGroup: g.Name})
			if err != nil || len(out) == 0 {
				msg := "no instance returned"
				if err != nil {
					msg = apierr.As(err).Message
				}
				s.activity(ctx, g, "Launching a new EC2 instance", "Launch failed: "+msg, "Failed")
				break
			}
			id := out[0].InstanceID
			perSubnet[subnet]++
			launched = append(launched, id)
			for _, tg := range g.TargetGroupIDs {
				if err := s.ELB.RegisterInstance(ctx, g.AccountID, tg, id); err != nil {
					s.Log.Warn("register auto scaling instance", "tg", tg, "err", err)
				}
			}
			s.activity(ctx, g, "Launching a new EC2 instance: "+id,
				fmt.Sprintf("The desired capacity is %d and the group had %d healthy instance(s); launching into %s", g.DesiredCapacity, len(active)+n, subnet), "Successful")
		}
	}
	nz := func(x []string) []string {
		if x == nil {
			return []string{}
		}
		return x
	}
	result["replace"], result["launched"], result["terminated"] = nz(replaced), nz(launched), nz(terminated)
	return result, nil
}

// OnInstanceTerminated records an activity when a group member is terminated outside the controller.
func (s *AutoScalingService) OnInstanceTerminated(ctx context.Context, account, instanceID string) {
	inst, err := s.Store.EC2.GetInstance(ctx, account, instanceID)
	if err != nil || inst.AutoScalingGroup == "" || !strings.Contains(inst.StateReason, "User initiated") {
		return
	}
	if g, err := s.Repo.GetGroup(ctx, account, inst.AutoScalingGroup); err == nil {
		s.activity(ctx, g, "Instance "+instanceID+" terminated", "The instance was terminated by a user request; it will be replaced on the next evaluation", "Successful")
	}
}

// CollectMetrics produces CloudSim/AutoScaling group metrics and per-group EC2 CPU.
func (s *AutoScalingService) CollectMetrics(ctx context.Context, samples map[string]simulator.Sample) []repositories.MetricDatum {
	gs, err := s.Repo.ListGroups(ctx, "")
	if err != nil {
		return nil
	}
	var out []repositories.MetricDatum
	for _, g := range gs {
		insts, err := s.members(ctx, &g)
		if err != nil {
			continue
		}
		inService, n, cpu := 0, 0, 0.0
		for _, i := range insts {
			if i.State == "running" {
				inService++
			}
			if sm, ok := samples[i.InstanceID]; ok {
				n++
				cpu += sm.CPUPercent
			}
		}
		dk := repositories.CanonicalDims(map[string]string{"AutoScalingGroupName": g.Name})
		out = append(out,
			repositories.MetricDatum{AccountID: g.AccountID, Namespace: NSASG, MetricName: "GroupDesiredCapacity", DimKey: dk, Value: float64(g.DesiredCapacity), Unit: "Count"},
			repositories.MetricDatum{AccountID: g.AccountID, Namespace: NSASG, MetricName: "GroupInServiceInstances", DimKey: dk, Value: float64(inService), Unit: "Count"},
			repositories.MetricDatum{AccountID: g.AccountID, Namespace: NSASG, MetricName: "GroupMinSize", DimKey: dk, Value: float64(g.MinSize), Unit: "Count"},
			repositories.MetricDatum{AccountID: g.AccountID, Namespace: NSASG, MetricName: "GroupMaxSize", DimKey: dk, Value: float64(g.MaxSize), Unit: "Count"},
		)
		if n > 0 {
			out = append(out, repositories.MetricDatum{AccountID: g.AccountID, Namespace: NSEC2, MetricName: "CPUUtilization", DimKey: dk, Value: cpu / float64(n), Unit: "Percent"})
		}
	}
	return out
}

// Resync re-registers groups with the engine.
func (s *AutoScalingService) Resync(ctx context.Context) error {
	gs, err := s.Repo.ListGroups(ctx, "")
	if err != nil {
		return err
	}
	for _, g := range gs {
		if _, err := s.Engine.Register(ctx, g.GroupID, simulator.RegisterRequest{Kind: "autoscaling_group", Owner: g.AccountID, State: strPtr("available"),
			DependsOn: g.SubnetIDs, Attributes: map[string]any{"name": g.Name}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *AutoScalingService) CountGroups(ctx context.Context, account string) int {
	l, _ := s.Repo.ListGroups(ctx, account)
	return len(l)
}

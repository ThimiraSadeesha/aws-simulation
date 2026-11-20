package services

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/policy"
	"cloudsim/backend/internal/repositories"
)

type IAMService struct {
	*Deps
	Auth *auth.Service
}

func NewIAMService(d *Deps, a *auth.Service) *IAMService { return &IAMService{Deps: d, Auth: a} }

var iamNameRe = regexp.MustCompile(`^[\w+=,.@-]{1,64}$`)

func validName(kind, n string) error {
	if !iamNameRe.MatchString(n) {
		return apierr.Validation("%s name must be 1-64 characters of letters, digits and +=,.@_-", kind)
	}
	return nil
}

func (s *IAMService) changed() { s.Auth.InvalidatePolicies() }

// Builtin managed policies (read-only, shared by all accounts).
var builtinPolicies = []struct{ name, desc, doc string }{
	{"AdministratorAccess", "Full access to all simulated services", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`},
	{"ReadOnlyAccess", "Read-only access to all simulated services", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["*:Describe*","*:Get*","*:List*"],"Resource":"*"}]}`},
	{"CloudSimEC2FullAccess", "Full access to EC2 and VPC networking", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:*","elasticloadbalancing:*","autoscaling:*","cloudwatch:*"],"Resource":"*"}]}`},
	{"CloudSimEC2ReadOnlyAccess", "Read-only access to EC2", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:Describe*","ec2:Get*","elasticloadbalancing:Describe*","autoscaling:Describe*","cloudwatch:Describe*","cloudwatch:Get*","cloudwatch:List*"],"Resource":"*"}]}`},
	{"CloudSimS3FullAccess", "Full access to S3", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
	{"CloudSimS3ReadOnlyAccess", "Read-only access to S3", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":"*"}]}`},
	{"IAMFullAccess", "Full access to IAM", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["iam:*","sts:*"],"Resource":"*"}]}`},
	{"IAMReadOnlyAccess", "Read-only access to IAM", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["iam:Get*","iam:List*","iam:Simulate*"],"Resource":"*"}]}`},
	{"CloudWatchReadOnlyAccess", "Read-only access to CloudWatch", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["cloudwatch:Describe*","cloudwatch:Get*","cloudwatch:List*","logs:Describe*","logs:Get*","logs:Filter*"],"Resource":"*"}]}`},
}

// SeedBuiltins installs/updates builtin managed policies.
func (s *IAMService) SeedBuiltins(ctx context.Context) error {
	for _, b := range builtinPolicies {
		p := &repositories.IAMPolicy{PolicyName: b.name, PolicyID: "ANPA" + strings.ToUpper(strings.ReplaceAll(b.name, "Access", ""))[:min(17, len(strings.ReplaceAll(b.name, "Access", "")))],
			ARN: "arn:cloudsim:iam::cloudsim:policy/" + b.name, Description: b.desc, Document: json.RawMessage(b.doc)}
		if err := s.Store.IAM.UpsertBuiltinPolicy(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// ---- users

type CreateUserInput struct {
	UserName string            `json:"user_name"`
	Path     string            `json:"path"`
	Tags     map[string]string `json:"tags"`
}

func normPath(p string) (string, error) {
	if p == "" {
		return "/", nil
	}
	if !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") || len(p) > 512 {
		return "", apierr.Validation("path must begin and end with '/'")
	}
	return p, nil
}

func (s *IAMService) CreateUser(ctx context.Context, account string, in CreateUserInput) (*repositories.IAMUser, error) {
	if err := validName("user", in.UserName); err != nil {
		return nil, err
	}
	path, err := normPath(in.Path)
	if err != nil {
		return nil, err
	}
	if err := ValidateTags(in.Tags); err != nil {
		return nil, err
	}
	u := &repositories.IAMUser{AccountID: account, UserName: in.UserName, UserID: ids.Unique("AIDA"), Path: path, Tags: in.Tags}
	if u.Tags == nil {
		u.Tags = map[string]string{}
	}
	if err := s.Store.IAM.CreateUser(ctx, u); err != nil {
		return nil, RepoErr(err, "user "+in.UserName)
	}
	s.RecordEvent(ctx, account, u.ARN, "iam_user", "created", "IAM user "+u.UserName+" created", nil)
	return u, nil
}

func (s *IAMService) GetUser(ctx context.Context, account, name string) (*repositories.IAMUser, error) {
	u, err := s.Store.IAM.GetUser(ctx, account, name)
	return u, RepoErr(err, "user "+name)
}

type UserDetail struct {
	repositories.IAMUser
	Groups           []repositories.IAMGroup     `json:"groups"`
	AttachedPolicies []repositories.IAMPolicy    `json:"attached_policies"`
	InlinePolicies   []repositories.InlinePolicy `json:"inline_policies"`
	AccessKeys       []repositories.AccessKey    `json:"access_keys"`
}

func (s *IAMService) UserDetail(ctx context.Context, account, name string) (*UserDetail, error) {
	u, err := s.GetUser(ctx, account, name)
	if err != nil {
		return nil, err
	}
	d := &UserDetail{IAMUser: *u}
	if d.Groups, err = s.Store.IAM.GroupsForUser(ctx, u.ID); err != nil {
		return nil, err
	}
	if d.AttachedPolicies, err = s.Store.IAM.AttachedPolicies(ctx, repositories.PUser, u.ID); err != nil {
		return nil, err
	}
	if d.InlinePolicies, err = s.Store.IAM.InlinePolicies(ctx, repositories.PUser, u.ID); err != nil {
		return nil, err
	}
	if d.AccessKeys, err = s.Store.IAM.ListAccessKeys(ctx, u.ID); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *IAMService) DeleteUser(ctx context.Context, account, name string) error {
	u, err := s.GetUser(ctx, account, name)
	if err != nil {
		return err
	}
	if err := s.Store.IAM.DeleteUser(ctx, u.ID); err != nil {
		return err
	}
	s.changed()
	s.RecordEvent(ctx, account, u.ARN, "iam_user", "deleted", "IAM user "+name+" deleted", nil)
	return nil
}

type NewAccessKey struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Status          string `json:"status"`
	UserName        string `json:"user_name"`
	CreatedAt       string `json:"created_at"`
}

func (s *IAMService) CreateAccessKey(ctx context.Context, account, userName string) (*NewAccessKey, error) {
	u, err := s.GetUser(ctx, account, userName)
	if err != nil {
		return nil, err
	}
	keys, err := s.Store.IAM.ListAccessKeys(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if len(keys) >= 2 {
		return nil, apierr.New(409, "LimitExceeded", "user %s already has the maximum of 2 access keys", userName)
	}
	secret := ids.Secret()
	k := &repositories.AccessKey{AccessKeyID: ids.AccessKeyID(false), AccountID: account, IAMUserID: &u.ID, SecretHash: auth.HashToken(secret), Status: "Active"}
	if err := s.Store.IAM.CreateAccessKey(ctx, k); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, u.ARN, "iam_user", "access_key_created", "Access key "+k.AccessKeyID+" created for "+userName, nil)
	return &NewAccessKey{AccessKeyID: k.AccessKeyID, SecretAccessKey: secret, Status: k.Status, UserName: userName, CreatedAt: k.CreatedAt}, nil
}

func (s *IAMService) userKey(ctx context.Context, account, userName, keyID string) (*repositories.AccessKey, error) {
	u, err := s.GetUser(ctx, account, userName)
	if err != nil {
		return nil, err
	}
	k, err := s.Store.IAM.GetAccessKey(ctx, keyID)
	if err != nil || k.IAMUserID == nil || *k.IAMUserID != u.ID {
		return nil, apierr.NotFound("access key %s not found for user %s", keyID, userName)
	}
	return k, nil
}

func (s *IAMService) SetAccessKeyStatus(ctx context.Context, account, userName, keyID, status string) error {
	if status != "Active" && status != "Inactive" {
		return apierr.Validation("status must be Active or Inactive")
	}
	if _, err := s.userKey(ctx, account, userName, keyID); err != nil {
		return err
	}
	return s.Store.IAM.SetAccessKeyStatus(ctx, keyID, status)
}

func (s *IAMService) DeleteAccessKey(ctx context.Context, account, userName, keyID string) error {
	if _, err := s.userKey(ctx, account, userName, keyID); err != nil {
		return err
	}
	return s.Store.IAM.DeleteAccessKey(ctx, keyID)
}

// ---- groups

func (s *IAMService) CreateGroup(ctx context.Context, account, name, path string) (*repositories.IAMGroup, error) {
	if err := validName("group", name); err != nil {
		return nil, err
	}
	p, err := normPath(path)
	if err != nil {
		return nil, err
	}
	g := &repositories.IAMGroup{AccountID: account, GroupName: name, GroupID: ids.Unique("AGPA"), Path: p}
	if err := s.Store.IAM.CreateGroup(ctx, g); err != nil {
		return nil, RepoErr(err, "group "+name)
	}
	s.RecordEvent(ctx, account, g.ARN, "iam_group", "created", "IAM group "+name+" created", nil)
	return g, nil
}

type GroupDetail struct {
	repositories.IAMGroup
	Users            []repositories.IAMUser      `json:"users"`
	AttachedPolicies []repositories.IAMPolicy    `json:"attached_policies"`
	InlinePolicies   []repositories.InlinePolicy `json:"inline_policies"`
}

func (s *IAMService) GroupDetail(ctx context.Context, account, name string) (*GroupDetail, error) {
	g, err := s.Store.IAM.GetGroup(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "group "+name)
	}
	d := &GroupDetail{IAMGroup: *g}
	if d.Users, err = s.Store.IAM.GroupMembers(ctx, g.ID); err != nil {
		return nil, err
	}
	if d.AttachedPolicies, err = s.Store.IAM.AttachedPolicies(ctx, repositories.PGroup, g.ID); err != nil {
		return nil, err
	}
	d.InlinePolicies, err = s.Store.IAM.InlinePolicies(ctx, repositories.PGroup, g.ID)
	return d, err
}

func (s *IAMService) DeleteGroup(ctx context.Context, account, name string) error {
	g, err := s.Store.IAM.GetGroup(ctx, account, name)
	if err != nil {
		return RepoErr(err, "group "+name)
	}
	members, _ := s.Store.IAM.GroupMembers(ctx, g.ID)
	if len(members) > 0 {
		return apierr.Dependency("group %s still has %d member(s)", name, len(members))
	}
	if err := s.Store.IAM.DeleteGroup(ctx, g.ID); err != nil {
		return err
	}
	s.changed()
	return nil
}

func (s *IAMService) Membership(ctx context.Context, account, group, user string, add bool) error {
	g, err := s.Store.IAM.GetGroup(ctx, account, group)
	if err != nil {
		return RepoErr(err, "group "+group)
	}
	u, err := s.GetUser(ctx, account, user)
	if err != nil {
		return err
	}
	if add {
		err = s.Store.IAM.AddMember(ctx, g.ID, u.ID)
	} else {
		err = s.Store.IAM.RemoveMember(ctx, g.ID, u.ID)
	}
	s.changed()
	return err
}

// ---- roles

type CreateRoleInput struct {
	RoleName           string          `json:"role_name"`
	Path               string          `json:"path"`
	Description        string          `json:"description"`
	AssumeRolePolicy   json.RawMessage `json:"assume_role_policy_document"`
	MaxSessionDuration int             `json:"max_session_duration"`
}

func (s *IAMService) CreateRole(ctx context.Context, account string, in CreateRoleInput) (*repositories.IAMRole, error) {
	if err := validName("role", in.RoleName); err != nil {
		return nil, err
	}
	path, err := normPath(in.Path)
	if err != nil {
		return nil, err
	}
	if len(in.AssumeRolePolicy) == 0 {
		return nil, apierr.Validation("assume_role_policy_document is required")
	}
	doc := policyText(in.AssumeRolePolicy)
	if errs := policy.Validate(doc, policy.Trust); len(errs) > 0 {
		return nil, apierr.New(400, "MalformedPolicyDocument", "%s", strings.Join(errs, "; "))
	}
	if in.MaxSessionDuration == 0 {
		in.MaxSessionDuration = 3600
	}
	if in.MaxSessionDuration < 900 || in.MaxSessionDuration > 43200 {
		return nil, apierr.Validation("max_session_duration must be 900-43200 seconds")
	}
	r := &repositories.IAMRole{AccountID: account, RoleName: in.RoleName, RoleID: ids.Unique("AROA"), Path: path, Description: in.Description,
		AssumeRolePolicy: json.RawMessage(doc), MaxSessionDuration: in.MaxSessionDuration}
	if err := s.Store.IAM.CreateRole(ctx, r); err != nil {
		return nil, RepoErr(err, "role "+in.RoleName)
	}
	s.RecordEvent(ctx, account, r.ARN, "iam_role", "created", "IAM role "+r.RoleName+" created", nil)
	return r, nil
}

// policyText accepts a policy given as a JSON object or as a JSON string containing the document.
func policyText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	return string(raw)
}

type RoleDetail struct {
	repositories.IAMRole
	AttachedPolicies []repositories.IAMPolicy    `json:"attached_policies"`
	InlinePolicies   []repositories.InlinePolicy `json:"inline_policies"`
}

func (s *IAMService) RoleDetail(ctx context.Context, account, name string) (*RoleDetail, error) {
	r, err := s.Store.IAM.GetRole(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "role "+name)
	}
	d := &RoleDetail{IAMRole: *r}
	if d.AttachedPolicies, err = s.Store.IAM.AttachedPolicies(ctx, repositories.PRole, r.ID); err != nil {
		return nil, err
	}
	d.InlinePolicies, err = s.Store.IAM.InlinePolicies(ctx, repositories.PRole, r.ID)
	return d, err
}

func (s *IAMService) DeleteRole(ctx context.Context, account, name string) error {
	r, err := s.Store.IAM.GetRole(ctx, account, name)
	if err != nil {
		return RepoErr(err, "role "+name)
	}
	if err := s.Store.IAM.DeleteRole(ctx, r.ID); err != nil {
		return err
	}
	s.changed()
	return nil
}

// ---- managed policies

type CreatePolicyInput struct {
	PolicyName  string          `json:"policy_name"`
	Description string          `json:"description"`
	Document    json.RawMessage `json:"policy_document"`
}

func (s *IAMService) CreatePolicy(ctx context.Context, account string, in CreatePolicyInput) (*repositories.IAMPolicy, error) {
	if err := validName("policy", in.PolicyName); err != nil {
		return nil, err
	}
	doc := policyText(in.Document)
	if errs := policy.Validate(doc, policy.Identity); len(errs) > 0 {
		return nil, apierr.New(400, "MalformedPolicyDocument", "%s", strings.Join(errs, "; "))
	}
	if len(doc) > 6144 {
		return nil, apierr.Validation("policy documents are limited to 6144 characters")
	}
	if _, err := s.Store.IAM.GetPolicy(ctx, account, in.PolicyName); err == nil {
		return nil, apierr.Conflict("policy %s already exists", in.PolicyName)
	}
	p := &repositories.IAMPolicy{AccountID: account, PolicyName: in.PolicyName, PolicyID: ids.Unique("ANPA"),
		ARN: "arn:cloudsim:iam::" + account + ":policy/" + in.PolicyName, Description: in.Description, Document: json.RawMessage(doc)}
	if err := s.Store.IAM.CreatePolicy(ctx, p); err != nil {
		return nil, RepoErr(err, "policy "+in.PolicyName)
	}
	s.RecordEvent(ctx, account, p.ARN, "iam_policy", "created", "IAM policy "+p.PolicyName+" created", nil)
	return p, nil
}

func (s *IAMService) GetPolicy(ctx context.Context, account, nameOrARN string) (*repositories.IAMPolicy, error) {
	p, err := s.Store.IAM.GetPolicy(ctx, account, nameOrARN)
	return p, RepoErr(err, "policy "+nameOrARN)
}

func (s *IAMService) UpdatePolicy(ctx context.Context, account, nameOrARN string, docRaw json.RawMessage) (*repositories.IAMPolicy, error) {
	p, err := s.GetPolicy(ctx, account, nameOrARN)
	if err != nil {
		return nil, err
	}
	if p.Builtin {
		return nil, apierr.Denied("builtin managed policies cannot be modified")
	}
	doc := policyText(docRaw)
	if errs := policy.Validate(doc, policy.Identity); len(errs) > 0 {
		return nil, apierr.New(400, "MalformedPolicyDocument", "%s", strings.Join(errs, "; "))
	}
	if err := s.Store.IAM.UpdatePolicyDocument(ctx, p.ID, doc); err != nil {
		return nil, err
	}
	s.changed()
	return s.GetPolicy(ctx, account, p.ARN)
}

func (s *IAMService) DeletePolicy(ctx context.Context, account, nameOrARN string) error {
	p, err := s.GetPolicy(ctx, account, nameOrARN)
	if err != nil {
		return err
	}
	if p.Builtin {
		return apierr.Denied("builtin managed policies cannot be deleted")
	}
	if p.Attachments > 0 {
		return apierr.Dependency("policy %s is attached to %d principal(s); detach it first", p.PolicyName, p.Attachments)
	}
	return s.Store.IAM.DeletePolicy(ctx, p.ID)
}

// principal resolves (kind, name) to the internal id.
func (s *IAMService) principal(ctx context.Context, account, kind, name string) (string, int64, string, error) {
	switch kind {
	case "users":
		u, err := s.GetUser(ctx, account, name)
		if err != nil {
			return "", 0, "", err
		}
		return repositories.PUser, u.ID, u.ARN, nil
	case "groups":
		g, err := s.Store.IAM.GetGroup(ctx, account, name)
		if err != nil {
			return "", 0, "", RepoErr(err, "group "+name)
		}
		return repositories.PGroup, g.ID, g.ARN, nil
	case "roles":
		r, err := s.Store.IAM.GetRole(ctx, account, name)
		if err != nil {
			return "", 0, "", RepoErr(err, "role "+name)
		}
		return repositories.PRole, r.ID, r.ARN, nil
	}
	return "", 0, "", apierr.NotFound("unknown principal type %s", kind)
}

func (s *IAMService) AttachPolicy(ctx context.Context, account, kind, name, policyName string, attach bool) error {
	ptype, pid, arn, err := s.principal(ctx, account, kind, name)
	if err != nil {
		return err
	}
	p, err := s.GetPolicy(ctx, account, policyName)
	if err != nil {
		return err
	}
	if attach {
		err = s.Store.IAM.Attach(ctx, p.ID, account, ptype, pid)
	} else {
		err = RepoErr(s.Store.IAM.Detach(ctx, p.ID, ptype, pid), "attachment")
	}
	if err != nil {
		return err
	}
	s.changed()
	verb := "attached to"
	if !attach {
		verb = "detached from"
	}
	s.RecordEvent(ctx, account, arn, "iam_"+ptype, "policy_"+map[bool]string{true: "attached", false: "detached"}[attach], "Policy "+p.PolicyName+" "+verb+" "+name, nil)
	return nil
}

func (s *IAMService) PutInline(ctx context.Context, account, kind, name, policyName string, docRaw json.RawMessage) error {
	if err := validName("policy", policyName); err != nil {
		return err
	}
	ptype, pid, _, err := s.principal(ctx, account, kind, name)
	if err != nil {
		return err
	}
	doc := policyText(docRaw)
	if errs := policy.Validate(doc, policy.Identity); len(errs) > 0 {
		return apierr.New(400, "MalformedPolicyDocument", "%s", strings.Join(errs, "; "))
	}
	if err := s.Store.IAM.PutInline(ctx, account, ptype, pid, policyName, doc); err != nil {
		return err
	}
	s.changed()
	return nil
}

func (s *IAMService) DeleteInline(ctx context.Context, account, kind, name, policyName string) error {
	ptype, pid, _, err := s.principal(ctx, account, kind, name)
	if err != nil {
		return err
	}
	if err := RepoErr(s.Store.IAM.DeleteInline(ctx, ptype, pid, policyName), "inline policy "+policyName); err != nil {
		return err
	}
	s.changed()
	return nil
}

// ---- simulation & STS

type SimulateInput struct {
	PrincipalARN string            `json:"principal_arn"`
	Action       string            `json:"action"`
	ResourceARN  string            `json:"resource_arn"`
	Context      map[string]string `json:"context"`
}

// Simulate evaluates a request for a user or role in the caller's account.
func (s *IAMService) Simulate(ctx context.Context, account string, in SimulateInput) (*policy.Result, error) {
	if in.Action == "" {
		return nil, apierr.Validation("action is required")
	}
	if in.ResourceARN == "" {
		in.ResourceARN = "*"
	}
	prefix := "arn:cloudsim:iam::" + account + ":"
	if !strings.HasPrefix(in.PrincipalARN, prefix) {
		return nil, apierr.Validation("principal_arn must be a user or role ARN in account %s", account)
	}
	rest := strings.TrimPrefix(in.PrincipalARN, prefix)
	var p auth.Principal
	p.AccountID = account
	p.ARN = in.PrincipalARN
	switch {
	case strings.HasPrefix(rest, "user/"):
		name := rest[strings.LastIndex(rest, "/")+1:]
		u, err := s.GetUser(ctx, account, name)
		if err != nil {
			return nil, err
		}
		p.Kind, p.IAMUserID, p.Name = auth.KindIAMUser, u.ID, u.UserName
	case strings.HasPrefix(rest, "role/"):
		name := rest[strings.LastIndex(rest, "/")+1:]
		r, err := s.Store.IAM.GetRole(ctx, account, name)
		if err != nil {
			return nil, RepoErr(err, "role "+name)
		}
		p.Kind, p.RoleID, p.Name = auth.KindAssumedRole, r.ID, r.RoleName
	default:
		return nil, apierr.Validation("principal_arn must reference user/<name> or role/<name>")
	}
	s.Auth.InvalidatePolicies()
	res, err := s.Auth.Authorize(ctx, &p, in.Action, in.ResourceARN, nil, in.Context)
	return &res, err
}

type AssumeRoleInput struct {
	RoleARN         string `json:"role_arn"`
	RoleSessionName string `json:"role_session_name"`
	DurationSeconds int    `json:"duration_seconds"`
}

var sessionNameRe = regexp.MustCompile(`^[\w+=,.@-]{2,64}$`)

// AssumeRole issues temporary credentials if the caller is trusted by the role.
func (s *IAMService) AssumeRole(ctx context.Context, caller *auth.Principal, in AssumeRoleInput) (map[string]any, error) {
	if !sessionNameRe.MatchString(in.RoleSessionName) {
		return nil, apierr.Validation("role_session_name must be 2-64 characters of letters, digits and +=,.@_-")
	}
	name := in.RoleARN[strings.LastIndex(in.RoleARN, "/")+1:]
	if !strings.HasPrefix(in.RoleARN, "arn:cloudsim:iam::"+caller.AccountID+":role/") {
		return nil, apierr.Denied("cross-account role assumption is not supported in the simulator")
	}
	r, err := s.Store.IAM.GetRole(ctx, caller.AccountID, name)
	if err != nil {
		return nil, RepoErr(err, "role "+name)
	}
	trust, err := policy.Parse(string(r.AssumeRolePolicy))
	if err != nil {
		return nil, err
	}
	if caller.Kind != auth.KindRoot && !policy.EvaluateTrust(trust, caller.ARN, caller.AccountID) {
		return nil, apierr.Denied("%s is not authorized to perform sts:AssumeRole on %s: the role's trust policy does not allow this principal", caller.ARN, r.ARN)
	}
	if in.DurationSeconds == 0 {
		in.DurationSeconds = 3600
	}
	if in.DurationSeconds < 900 || in.DurationSeconds > r.MaxSessionDuration {
		return nil, apierr.Validation("duration_seconds must be between 900 and the role's max_session_duration (%d)", r.MaxSessionDuration)
	}
	secret, token := ids.Secret(), ids.Token()
	exp := db.Fmt(time.Now().Add(time.Duration(in.DurationSeconds) * time.Second))
	k := &repositories.AccessKey{AccessKeyID: ids.AccessKeyID(true), AccountID: caller.AccountID, RoleID: &r.ID, SessionName: in.RoleSessionName,
		SecretHash: auth.HashToken(secret), SessionTokenHash: auth.HashToken(token), Status: "Active", ExpiresAt: &exp}
	if err := s.Store.IAM.CreateAccessKey(ctx, k); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, caller.AccountID, r.ARN, "iam_role", "assumed", caller.Display()+" assumed role "+r.RoleName+" (session "+in.RoleSessionName+")", nil)
	return map[string]any{
		"credentials":       map[string]string{"access_key_id": k.AccessKeyID, "secret_access_key": secret, "session_token": token, "expiration": exp},
		"assumed_role_user": map[string]string{"arn": "arn:cloudsim:sts::" + caller.AccountID + ":assumed-role/" + r.RoleName + "/" + in.RoleSessionName, "assumed_role_id": r.RoleID + ":" + in.RoleSessionName},
	}, nil
}

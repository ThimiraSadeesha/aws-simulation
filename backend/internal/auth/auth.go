// Package auth handles platform authentication (sessions, passwords, access keys)
// and authorization (IAM policy evaluation for IAM principals).
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/policy"
	"cloudsim/backend/internal/repositories"
)

// Principal kinds.
const (
	KindRoot        = "root"
	KindIAMUser     = "iam_user"
	KindAssumedRole = "assumed_role"
)

// Principal is the authenticated caller of a request.
type Principal struct {
	Kind         string
	AccountID    string
	PlatformUser *repositories.User // always set (owner of the account)
	IAMUserID    int64
	RoleID       int64
	Name         string // IAM user name, or role/session
	ARN          string
	AccessKeyID  string
	ViaSession   bool
	CSRFToken    string
	SessionHash  string
}

// Display returns a short audit-friendly identity.
func (p *Principal) Display() string {
	switch p.Kind {
	case KindRoot:
		return "root"
	case KindIAMUser:
		return "iam-user/" + p.Name
	default:
		return "assumed-role/" + p.Name
	}
}

// IsAdmin reports whether the caller holds the platform admin role (root sessions only).
func (p *Principal) IsAdmin() bool {
	return p.Kind == KindRoot && p.PlatformUser != nil && p.PlatformUser.Role == "admin"
}

func HashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

type Service struct {
	Store      *repositories.Store
	SessionTTL time.Duration
	BcryptCost int

	cacheMu sync.RWMutex
	cache   map[string]cachedPolicies // principal key -> parsed policies
}

type cachedPolicies struct {
	at   time.Time
	pols []policy.Named
}

func NewService(store *repositories.Store, ttl time.Duration) *Service {
	return &Service{Store: store, SessionTTL: ttl, BcryptCost: bcrypt.DefaultCost, cache: map[string]cachedPolicies{}}
}

// InvalidatePolicies clears the policy cache (call after any IAM mutation).
func (s *Service) InvalidatePolicies() {
	s.cacheMu.Lock()
	s.cache = map[string]cachedPolicies{}
	s.cacheMu.Unlock()
}

// Register creates a platform user. The first user becomes admin.
func (s *Service) Register(ctx context.Context, username, password, email string, allowRegistration bool) (*repositories.User, error) {
	if !usernameRe.MatchString(username) {
		return nil, apierr.Validation("username must be 3-32 characters of letters, digits, '.', '_' or '-'")
	}
	if len(password) < 8 || len(password) > 128 {
		return nil, apierr.Validation("password must be 8-128 characters")
	}
	if len(email) > 254 || (email != "" && !strings.Contains(email, "@")) {
		return nil, apierr.Validation("invalid email")
	}
	n, err := s.Store.Users.Count(ctx)
	if err != nil {
		return nil, err
	}
	if n > 0 && !allowRegistration {
		return nil, apierr.New(403, "RegistrationDisabled", "self-registration is disabled; ask an administrator")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.BcryptCost)
	if err != nil {
		return nil, err
	}
	role := "user"
	if n == 0 {
		role = "admin"
	}
	if _, err := s.Store.Users.GetByUsername(ctx, username); err == nil {
		return nil, apierr.Conflict("username already taken")
	}
	u := &repositories.User{Username: username, Email: email, PasswordHash: string(hash), Role: role, AccountID: ids.AccountID()}
	if err := s.Store.Users.Create(ctx, u); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, apierr.Conflict("username already taken")
		}
		return nil, err
	}
	return u, nil
}

// dummyHash equalizes timing between unknown-user and wrong-password paths.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("cloudsim-timing-equalizer"), bcrypt.DefaultCost)

// Login verifies credentials and creates a session. Returns raw session token and csrf token.
func (s *Service) Login(ctx context.Context, username, password, ip, ua string) (*repositories.User, string, string, error) {
	u, err := s.Store.Users.GetByUsername(ctx, username)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, "", "", apierr.Unauthenticated("invalid username or password")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, "", "", apierr.Unauthenticated("invalid username or password")
	}
	token, csrf := ids.Token(), ids.Token()
	sess := &repositories.Session{IDHash: HashToken(token), UserID: u.ID, CSRFToken: csrf, ExpiresAt: time.Now().Add(s.SessionTTL)}
	if len(ua) > 256 {
		ua = ua[:256]
	}
	if err := s.Store.Sessions.Create(ctx, sess, ip, ua); err != nil {
		return nil, "", "", err
	}
	return u, token, csrf, nil
}

// SessionPrincipal resolves a session cookie to the account root principal.
func (s *Service) SessionPrincipal(ctx context.Context, token string) (*Principal, error) {
	h := HashToken(token)
	sess, err := s.Store.Sessions.Get(ctx, h)
	if err != nil {
		return nil, apierr.Unauthenticated("session expired or invalid")
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = s.Store.Sessions.Delete(ctx, h)
		return nil, apierr.Unauthenticated("session expired")
	}
	u, err := s.Store.Users.GetByID(ctx, sess.UserID)
	if err != nil {
		return nil, apierr.Unauthenticated("session user no longer exists")
	}
	// Sliding expiration, written at most once per minute-ish of remaining lifetime change.
	if time.Until(sess.ExpiresAt) < s.SessionTTL-time.Minute {
		_ = s.Store.Sessions.Touch(ctx, h, time.Now().Add(s.SessionTTL))
	}
	return &Principal{
		Kind: KindRoot, AccountID: u.AccountID, PlatformUser: u, Name: u.Username,
		ARN: "arn:cloudsim:iam::" + u.AccountID + ":root", ViaSession: true, CSRFToken: sess.CSRFToken, SessionHash: h,
	}, nil
}

func (s *Service) Logout(ctx context.Context, p *Principal) error {
	if p.SessionHash == "" {
		return nil
	}
	return s.Store.Sessions.Delete(ctx, p.SessionHash)
}

// AccessKeyPrincipal authenticates "Authorization: CloudSim AKID:SECRET".
func (s *Service) AccessKeyPrincipal(ctx context.Context, keyID, secret, sessionToken string) (*Principal, error) {
	k, err := s.Store.IAM.GetAccessKey(ctx, keyID)
	if err != nil {
		return nil, apierr.Unauthenticated("the access key id does not exist")
	}
	if subtle.ConstantTimeCompare([]byte(HashToken(secret)), []byte(k.SecretHash)) != 1 {
		return nil, apierr.Unauthenticated("signature does not match (invalid secret access key)")
	}
	if k.Status != "Active" {
		return nil, apierr.Unauthenticated("the access key is inactive")
	}
	if k.ExpiresAt != nil && time.Now().After(db.Parse(*k.ExpiresAt)) {
		return nil, apierr.Unauthenticated("the security token included in the request is expired")
	}
	owner, err := s.Store.Users.GetByAccount(ctx, k.AccountID)
	if err != nil {
		return nil, apierr.Unauthenticated("account no longer exists")
	}
	p := &Principal{AccountID: k.AccountID, PlatformUser: owner, AccessKeyID: keyID}
	switch {
	case k.IAMUserID != nil:
		u, err := s.Store.IAM.GetUserByID(ctx, *k.IAMUserID)
		if err != nil {
			return nil, apierr.Unauthenticated("user no longer exists")
		}
		p.Kind, p.IAMUserID, p.Name, p.ARN = KindIAMUser, u.ID, u.UserName, u.ARN
	case k.RoleID != nil:
		if subtle.ConstantTimeCompare([]byte(HashToken(sessionToken)), []byte(k.SessionTokenHash)) != 1 {
			return nil, apierr.Unauthenticated("temporary credentials require a valid X-CloudSim-Session-Token")
		}
		r, err := s.Store.IAM.GetRoleByID(ctx, *k.RoleID)
		if err != nil {
			return nil, apierr.Unauthenticated("role no longer exists")
		}
		p.Kind, p.RoleID = KindAssumedRole, r.ID
		p.Name = r.RoleName + "/" + k.SessionName
		p.ARN = "arn:cloudsim:sts::" + r.AccountID + ":assumed-role/" + r.RoleName + "/" + k.SessionName
	default:
		return nil, errors.New("access key without principal")
	}
	go s.Store.IAM.TouchAccessKey(context.Background(), keyID) //nolint:errcheck // best-effort last-used tracking
	return p, nil
}

// IdentityPolicies loads and parses the effective identity policies for p (cached ~5s).
func (s *Service) IdentityPolicies(ctx context.Context, p *Principal) ([]policy.Named, error) {
	var ptype string
	var pid int64
	switch p.Kind {
	case KindIAMUser:
		ptype, pid = repositories.PUser, p.IAMUserID
	case KindAssumedRole:
		ptype, pid = repositories.PRole, p.RoleID
	default:
		return nil, nil
	}
	key := ptype + ":" + p.AccountID + ":" + itoa(pid)
	s.cacheMu.RLock()
	c, ok := s.cache[key]
	s.cacheMu.RUnlock()
	if ok && time.Since(c.at) < 5*time.Second {
		return c.pols, nil
	}
	rows, err := s.Store.IAM.EffectivePolicies(ctx, ptype, pid)
	if err != nil {
		return nil, err
	}
	out := make([]policy.Named, 0, len(rows))
	for _, r := range rows {
		d, err := policy.Parse(r.Document)
		if err != nil {
			continue // invalid documents are rejected at write time; skip defensively
		}
		out = append(out, policy.Named{Name: r.Name, Document: d})
	}
	s.cacheMu.Lock()
	s.cache[key] = cachedPolicies{at: time.Now(), pols: out}
	s.cacheMu.Unlock()
	return out, nil
}

// Authorize decides whether p may perform action on resource. Root principals are always allowed
// within their own account. resourcePolicies (e.g. bucket policies) are optional.
func (s *Service) Authorize(ctx context.Context, p *Principal, action, resource string, resourcePolicies []policy.Named, extra map[string]string) (policy.Result, error) {
	if p.Kind == KindRoot {
		return policy.Result{Decision: policy.Allowed, Matched: []policy.Match{{Policy: "account-root", Effect: "Allow"}}}, nil
	}
	pols, err := s.IdentityPolicies(ctx, p)
	if err != nil {
		return policy.Result{}, err
	}
	ctxKeys := map[string]string{
		"cloudsim:username":        p.Name,
		"cloudsim:PrincipalArn":    p.ARN,
		"cloudsim:RequestedRegion": "local-1",
	}
	for k, v := range extra {
		ctxKeys[k] = v
	}
	req := policy.Request{PrincipalARN: p.ARN, AccountID: p.AccountID, Action: action, Resource: resource, Context: ctxKeys}
	return policy.Evaluate(req, pols, resourcePolicies), nil
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ARN helpers.
func ARN(service, account, resource string) string {
	return "arn:cloudsim:" + service + ":local-1:" + account + ":" + resource
}

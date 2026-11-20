package services

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/policy"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

// S3Service implements S3-style object storage. Metadata lives in SQLite; object bytes are
// stored as opaque blobs under <dir>/<bucket>/<random blob id> (see s3_blobs.go).
//
// Documented simplifications:
//   - acl "public-read" lets any authenticated principal of the platform (any account) GetObject;
//     there is no anonymous access.
//   - Cross-account access is otherwise not modeled: other accounts' buckets are invisible (404),
//     even if a bucket policy names a foreign principal.
//   - Multipart parts are not required to be ≥ 5 MiB and the completed object is capped at MAX_OBJECT_SIZE.
type S3Service struct {
	*Deps
	Repo    repositories.S3Repo
	Dir     string
	MaxSize int64
}

func NewS3Service(d *Deps, repo repositories.S3Repo, dir string, maxSize int64) *S3Service {
	return &S3Service{Deps: d, Repo: repo, Dir: dir, MaxSize: maxSize}
}

const s3Region = "local-1"

// BucketARN / ObjectARN build S3 resource ARNs.
func BucketARN(bucket string) string      { return "arn:cloudsim:s3:::" + bucket }
func ObjectARN(bucket, key string) string { return "arn:cloudsim:s3:::" + bucket + "/" + key }

// ---- validation

var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// ValidateBucketName enforces DNS-compatible bucket naming rules.
func ValidateBucketName(name string) error {
	if len(name) < 3 || len(name) > 63 {
		return apierr.New(400, "InvalidBucketName", "bucket names must be between 3 and 63 characters long")
	}
	if !bucketNameRe.MatchString(name) {
		return apierr.New(400, "InvalidBucketName", "bucket names may contain only lowercase letters, digits, '.' and '-', and must begin and end with a letter or digit")
	}
	if strings.Contains(name, "..") || strings.Contains(name, ".-") || strings.Contains(name, "-.") {
		return apierr.New(400, "InvalidBucketName", "bucket names must not contain adjacent periods or a period next to a hyphen")
	}
	if ip := net.ParseIP(name); ip != nil && ip.To4() != nil {
		return apierr.New(400, "InvalidBucketName", "bucket names must not be formatted as an IP address")
	}
	if strings.HasPrefix(name, "xn--") || strings.HasSuffix(name, "-s3alias") {
		return apierr.New(400, "InvalidBucketName", "bucket names must not start with 'xn--' or end with '-s3alias'")
	}
	return nil
}

// ValidateKey enforces object key rules (1–1024 bytes of valid UTF-8).
func ValidateKey(key string) error {
	if key == "" || len(key) > 1024 {
		return apierr.New(400, "InvalidKey", "object keys must be 1-1024 bytes long")
	}
	if !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
		return apierr.New(400, "InvalidKey", "object keys must be valid UTF-8 without NUL characters")
	}
	return nil
}

var metaKeyRe = regexp.MustCompile(`^[a-z0-9-_.]{1,128}$`)

// ValidStorageClasses lists accepted storage classes.
var ValidStorageClasses = map[string]bool{"STANDARD": true, "STANDARD_IA": true, "ONEZONE_IA": true, "INTELLIGENT_TIERING": true,
	"GLACIER_IR": true, "GLACIER": true, "DEEP_ARCHIVE": true}

func validateObjectAttrs(contentType string, meta map[string]string, class string) error {
	if len(contentType) > 255 {
		return apierr.Validation("Content-Type too long")
	}
	n := 0
	for k, v := range meta {
		if !metaKeyRe.MatchString(k) {
			return apierr.Validation("invalid metadata key %q", k)
		}
		n += len(k) + len(v)
	}
	if n > 2048 {
		return apierr.New(400, "MetadataTooLarge", "user metadata must not exceed 2 KB")
	}
	if class != "" && !ValidStorageClasses[class] {
		return apierr.Validation("invalid storage class %q", class)
	}
	return nil
}

func noSuchBucket(name string) error {
	return apierr.New(404, "NotFound", "the specified bucket %s does not exist", name)
}

func noSuchKey(key string) error {
	return apierr.New(404, "NotFound", "the specified key %s does not exist", key)
}

// ---- buckets

type Transition struct {
	Days         int    `json:"days"`
	StorageClass string `json:"storage_class"`
}

type LifecycleRule struct {
	ID                           string       `json:"id"`
	Status                       string       `json:"status"`
	Prefix                       string       `json:"prefix"`
	ExpirationDays               *int         `json:"expiration_days,omitempty"`
	NoncurrentExpirationDays     *int         `json:"noncurrent_expiration_days,omitempty"`
	Transitions                  []Transition `json:"transitions,omitempty"`
	AbortIncompleteMultipartDays *int         `json:"abort_incomplete_multipart_days,omitempty"`
}

type BucketView struct {
	Name           string          `json:"name"`
	Region         string          `json:"region"`
	CreatedAt      string          `json:"created_at"`
	Versioning     string          `json:"versioning"`
	ACL            string          `json:"acl"`
	ObjectCount    int64           `json:"object_count"`
	SizeBytes      int64           `json:"size_bytes"`
	HasPolicy      bool            `json:"has_policy"`
	LifecycleRules []LifecycleRule `json:"lifecycle_rules"`
}

func rulesOf(b *repositories.S3Bucket) []LifecycleRule {
	rules := []LifecycleRule{}
	_ = json.Unmarshal(b.Lifecycle, &rules)
	if rules == nil {
		rules = []LifecycleRule{}
	}
	return rules
}

func viewOf(b *repositories.S3Bucket, u *repositories.S3Usage) BucketView {
	v := BucketView{Name: b.Name, Region: s3Region, CreatedAt: b.CreatedAt, Versioning: b.Versioning, ACL: b.ACL,
		HasPolicy: b.Policy != "", LifecycleRules: rulesOf(b)}
	if u != nil {
		v.ObjectCount, v.SizeBytes = u.Objects, u.SizeBytes
	}
	return v
}

// Lookup returns a bucket regardless of owner (used for authorization decisions).
func (s *S3Service) Lookup(ctx context.Context, name string) (*repositories.S3Bucket, error) {
	b, err := s.Repo.GetBucket(ctx, name)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, noSuchBucket(name)
	}
	return b, err
}

// GetBucket returns an account's bucket (other accounts' buckets → 404).
func (s *S3Service) GetBucket(ctx context.Context, account, name string) (*repositories.S3Bucket, error) {
	b, err := s.Lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	if b.AccountID != account {
		return nil, noSuchBucket(name)
	}
	return b, nil
}

func (s *S3Service) View(ctx context.Context, b *repositories.S3Bucket) (BucketView, error) {
	us, err := s.Repo.Usage(ctx, "", b.Name)
	if err != nil {
		return BucketView{}, err
	}
	var u *repositories.S3Usage
	if len(us) > 0 {
		u = &us[0]
	}
	return viewOf(b, u), nil
}

func (s *S3Service) ListBuckets(ctx context.Context, account string) ([]BucketView, error) {
	bs, err := s.Repo.ListBuckets(ctx, account)
	if err != nil {
		return nil, err
	}
	us, err := s.Repo.Usage(ctx, account, "")
	if err != nil {
		return nil, err
	}
	byName := map[string]*repositories.S3Usage{}
	for i := range us {
		byName[us[i].Bucket] = &us[i]
	}
	out := make([]BucketView, 0, len(bs))
	for i := range bs {
		out = append(out, viewOf(&bs[i], byName[bs[i].Name]))
	}
	return out, nil
}

type CreateBucketInput struct {
	Name string `json:"name"`
	ACL  string `json:"acl"`
}

func validACL(acl string) bool { return acl == "private" || acl == "public-read" }

func (s *S3Service) CreateBucket(ctx context.Context, account string, in CreateBucketInput) (BucketView, error) {
	if err := ValidateBucketName(in.Name); err != nil {
		return BucketView{}, err
	}
	if in.ACL == "" {
		in.ACL = "private"
	}
	if !validACL(in.ACL) {
		return BucketView{}, apierr.Validation("acl must be 'private' or 'public-read'")
	}
	if existing, err := s.Repo.GetBucket(ctx, in.Name); err == nil {
		if existing.AccountID == account {
			return BucketView{}, apierr.New(409, "BucketAlreadyOwnedByYou", "you already own bucket %s", in.Name)
		}
		return BucketView{}, apierr.New(409, "BucketAlreadyExists", "the bucket name %s is already taken; bucket names are globally unique", in.Name)
	}
	b := &repositories.S3Bucket{Name: in.Name, AccountID: account, ACL: in.ACL, Versioning: "Disabled"}
	// Insert first so the global-uniqueness race is decided by the primary key, then register with the engine.
	if err := s.Repo.InsertBucket(ctx, b); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return BucketView{}, apierr.New(409, "BucketAlreadyExists", "the bucket name %s is already taken; bucket names are globally unique", in.Name)
		}
		return BucketView{}, err
	}
	if _, err := s.Engine.Register(ctx, b.Name, simulator.RegisterRequest{Kind: "bucket", Owner: account, Attributes: map[string]any{"acl": b.ACL}}); err != nil {
		_, _ = s.Repo.DeleteBucket(ctx, b.Name)
		return BucketView{}, EngineErr(err)
	}
	if err := s.ensureBucketDir(b.Name); err != nil {
		return BucketView{}, err
	}
	s.RecordEvent(ctx, account, b.Name, "bucket", "created", "Bucket "+b.Name+" created", nil)
	return viewOf(b, nil), nil
}

func (s *S3Service) DeleteBucket(ctx context.Context, b *repositories.S3Bucket, force bool) error {
	if !force {
		has, err := s.Repo.HasContents(ctx, b.Name)
		if err != nil {
			return err
		}
		if has {
			return apierr.New(409, "BucketNotEmpty", "the bucket %s is not empty (delete all object versions and multipart uploads, or use ?force=true)", b.Name)
		}
	}
	if _, err := s.Repo.DeleteBucket(ctx, b.Name); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return noSuchBucket(b.Name)
		}
		return err
	}
	if err := s.removeBucketDir(b.Name); err != nil {
		s.Log.Error("remove bucket storage", "bucket", b.Name, "err", err)
	}
	if err := s.Engine.Deregister(ctx, b.Name); err != nil && !simulator.IsCode(err, "not_found") {
		s.Log.Warn("engine deregister bucket", "bucket", b.Name, "err", err)
	}
	msg := "Bucket " + b.Name + " deleted"
	if force {
		msg += " (force: all objects removed)"
	}
	s.RecordEvent(ctx, b.AccountID, b.Name, "bucket", "deleted", msg, nil)
	return nil
}

func (s *S3Service) SetVersioning(ctx context.Context, b *repositories.S3Bucket, status string) (BucketView, error) {
	if status != "Enabled" && status != "Suspended" {
		return BucketView{}, apierr.Validation("status must be 'Enabled' or 'Suspended' (versioning cannot be disabled once enabled)")
	}
	if err := s.Repo.SetBucketField(ctx, b.Name, "versioning", status); err != nil {
		return BucketView{}, err
	}
	if b.Versioning != status {
		s.RecordEvent(ctx, b.AccountID, b.Name, "bucket", "versioning_changed", "Versioning of "+b.Name+": "+b.Versioning+" → "+status, nil)
	}
	b.Versioning = status
	return s.View(ctx, b)
}

func (s *S3Service) SetACL(ctx context.Context, b *repositories.S3Bucket, acl string) (BucketView, error) {
	if !validACL(acl) {
		return BucketView{}, apierr.Validation("acl must be 'private' or 'public-read'")
	}
	if err := s.Repo.SetBucketField(ctx, b.Name, "acl", acl); err != nil {
		return BucketView{}, err
	}
	if b.ACL != acl {
		s.RecordEvent(ctx, b.AccountID, b.Name, "bucket", "acl_changed", "ACL of "+b.Name+" set to "+acl, nil)
	}
	b.ACL = acl
	_, _ = s.Engine.Register(ctx, b.Name, simulator.RegisterRequest{Kind: "bucket", Owner: b.AccountID, State: strPtr("available"), Attributes: map[string]any{"acl": acl}})
	return s.View(ctx, b)
}

// ---- bucket policy

// PolicyFor returns the parsed bucket policy as a resource policy ([] when none).
func (s *S3Service) PolicyFor(b *repositories.S3Bucket) []policy.Named {
	if b.Policy == "" {
		return nil
	}
	d, err := policy.Parse(b.Policy)
	if err != nil {
		return nil
	}
	return []policy.Named{{Name: "bucket-policy:" + b.Name, Document: d}}
}

func (s *S3Service) GetPolicy(b *repositories.S3Bucket) (json.RawMessage, error) {
	if b.Policy == "" {
		return nil, apierr.New(404, "NoSuchBucketPolicy", "the bucket %s does not have a bucket policy", b.Name)
	}
	return json.RawMessage(b.Policy), nil
}

func (s *S3Service) PutPolicy(ctx context.Context, b *repositories.S3Bucket, doc json.RawMessage) (json.RawMessage, error) {
	if len(doc) > 20<<10 {
		return nil, apierr.Validation("bucket policies are limited to 20 KB")
	}
	if errs := policy.Validate(string(doc), policy.Resource); len(errs) > 0 {
		return nil, apierr.New(400, "MalformedPolicy", "%s", strings.Join(errs, "; "))
	}
	d, _ := policy.Parse(string(doc))
	arn := BucketARN(b.Name)
	for i, st := range d.Statement {
		for _, r := range append(append([]string{}, st.Resource...), st.NotResource...) {
			rest, ok := strings.CutPrefix(r, arn)
			if !ok || !(rest == "" || strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "*")) {
				return nil, apierr.New(400, "MalformedPolicy", "Statement[%d]: resource %q must refer to this bucket (%s or %s/*)", i, r, arn, arn)
			}
		}
	}
	compact, err := json.Marshal(json.RawMessage(doc))
	if err != nil {
		return nil, apierr.Validation("invalid JSON")
	}
	if err := s.Repo.SetBucketField(ctx, b.Name, "policy", string(compact)); err != nil {
		return nil, err
	}
	b.Policy = string(compact)
	s.RecordEvent(ctx, b.AccountID, b.Name, "bucket", "policy_updated", "Bucket policy of "+b.Name+" updated", nil)
	return compact, nil
}

func (s *S3Service) DeletePolicy(ctx context.Context, b *repositories.S3Bucket) error {
	if err := s.Repo.SetBucketField(ctx, b.Name, "policy", ""); err != nil {
		return err
	}
	if b.Policy != "" {
		s.RecordEvent(ctx, b.AccountID, b.Name, "bucket", "policy_deleted", "Bucket policy of "+b.Name+" deleted", nil)
	}
	b.Policy = ""
	return nil
}

// ---- stats / metrics

type S3Stats struct {
	Buckets        int                    `json:"buckets"`
	Objects        int64                  `json:"objects"`
	Versions       int64                  `json:"versions"`
	SizeBytes      int64                  `json:"size_bytes"`
	ByBucket       []repositories.S3Usage `json:"by_bucket"`
	ByStorageClass map[string]int64       `json:"by_storage_class"`
}

func (s *S3Service) Stats(ctx context.Context, account string) (*S3Stats, error) {
	us, err := s.Repo.Usage(ctx, account, "")
	if err != nil {
		return nil, err
	}
	st := &S3Stats{Buckets: len(us), ByBucket: us}
	for _, u := range us {
		st.Objects += u.Objects
		st.Versions += u.Versions
		st.SizeBytes += u.SizeBytes
	}
	if st.ByStorageClass, err = s.Repo.StorageClassUsage(ctx, account); err != nil {
		return nil, err
	}
	return st, nil
}

// BucketSizes returns per-bucket object count and stored bytes (account "" = every account). It is the
// hook other modules (e.g. CloudWatch publishing cloudsim/S3 BucketSizeBytes / NumberOfObjects) use via
// s.Ext.Load("s3").
func (s *S3Service) BucketSizes(ctx context.Context, account string) ([]repositories.S3Usage, error) {
	return s.Repo.Usage(ctx, account, "")
}

// BucketAccount returns the owning account id of a bucket (for cross-module lookups).
func (s *S3Service) BucketAccount(ctx context.Context, bucket string) (string, error) {
	b, err := s.Lookup(ctx, bucket)
	if err != nil {
		return "", err
	}
	return b.AccountID, nil
}

// Resync re-registers every bucket with the engine.
func (s *S3Service) Resync(ctx context.Context) error {
	bs, err := s.Repo.AllBuckets(ctx)
	if err != nil {
		return err
	}
	for _, b := range bs {
		if _, err := s.Engine.Register(ctx, b.Name, simulator.RegisterRequest{Kind: "bucket", Owner: b.AccountID, State: strPtr("available"), Attributes: map[string]any{"acl": b.ACL}}); err != nil {
			return err
		}
	}
	return nil
}

// ---- object listing

type ObjectList struct {
	Items          []repositories.S3Object `json:"items"`
	CommonPrefixes []string                `json:"common_prefixes"`
	Prefix         string                  `json:"prefix"`
	Delimiter      string                  `json:"delimiter"`
	Total          int                     `json:"total"`
	Page           int                     `json:"page"`
	PageSize       int                     `json:"page_size"`
}

func normPage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 1000 {
		size = 1000
	}
	return page, size
}

func (s *S3Service) ListObjects(ctx context.Context, b *repositories.S3Bucket, prefix, delimiter, q string, page, size int) (*ObjectList, error) {
	if len(prefix) > 1024 || len(delimiter) > 16 || len(q) > 256 {
		return nil, apierr.Validation("prefix/delimiter/q too long")
	}
	page, size = normPage(page, size)
	objs, prefixes, total, err := s.Repo.ListObjects(ctx, repositories.S3ListQuery{Bucket: b.Name, Prefix: prefix, Delimiter: delimiter, Q: q, Offset: (page - 1) * size, Limit: size})
	if err != nil {
		return nil, err
	}
	return &ObjectList{Items: objs, CommonPrefixes: prefixes, Prefix: prefix, Delimiter: delimiter, Total: total, Page: page, PageSize: size}, nil
}

func (s *S3Service) ListVersions(ctx context.Context, b *repositories.S3Bucket, prefix string, page, size int) (*ObjectList, error) {
	if len(prefix) > 1024 {
		return nil, apierr.Validation("prefix too long")
	}
	page, size = normPage(page, size)
	objs, total, err := s.Repo.ListVersions(ctx, b.Name, prefix, (page-1)*size, size)
	if err != nil {
		return nil, err
	}
	return &ObjectList{Items: objs, CommonPrefixes: []string{}, Prefix: prefix, Total: total, Page: page, PageSize: size}, nil
}

// ---- lifecycle

func (s *S3Service) GetLifecycle(b *repositories.S3Bucket) map[string]any {
	return map[string]any{"rules": rulesOf(b)}
}

func (s *S3Service) PutLifecycle(ctx context.Context, b *repositories.S3Bucket, rules []LifecycleRule) (map[string]any, error) {
	if len(rules) > 1000 {
		return nil, apierr.Validation("at most 1000 lifecycle rules")
	}
	seen := map[string]bool{}
	pos := func(p *int, what string, i int) error {
		if p != nil && (*p < 1 || *p > 36500) {
			return apierr.Validation("rules[%d].%s must be between 1 and 36500 days", i, what)
		}
		return nil
	}
	for i := range rules {
		r := &rules[i]
		if r.ID == "" {
			r.ID = "rule-" + ids.Token()[:8]
		}
		if len(r.ID) > 255 || seen[r.ID] {
			return nil, apierr.Validation("rules[%d].id must be unique and at most 255 characters", i)
		}
		seen[r.ID] = true
		if r.Status == "" {
			r.Status = "Enabled"
		}
		if r.Status != "Enabled" && r.Status != "Disabled" {
			return nil, apierr.Validation("rules[%d].status must be Enabled or Disabled", i)
		}
		if len(r.Prefix) > 1024 {
			return nil, apierr.Validation("rules[%d].prefix too long", i)
		}
		if err := pos(r.ExpirationDays, "expiration_days", i); err != nil {
			return nil, err
		}
		if err := pos(r.NoncurrentExpirationDays, "noncurrent_expiration_days", i); err != nil {
			return nil, err
		}
		if err := pos(r.AbortIncompleteMultipartDays, "abort_incomplete_multipart_days", i); err != nil {
			return nil, err
		}
		for j, t := range r.Transitions {
			if t.Days < 0 || t.Days > 36500 {
				return nil, apierr.Validation("rules[%d].transitions[%d].days must be between 0 and 36500", i, j)
			}
			if !ValidStorageClasses[t.StorageClass] || t.StorageClass == "STANDARD" {
				return nil, apierr.Validation("rules[%d].transitions[%d].storage_class %q is not a valid transition target", i, j, t.StorageClass)
			}
		}
		if r.ExpirationDays == nil && r.NoncurrentExpirationDays == nil && len(r.Transitions) == 0 && r.AbortIncompleteMultipartDays == nil {
			return nil, apierr.Validation("rules[%d] must specify at least one action", i)
		}
	}
	if rules == nil {
		rules = []LifecycleRule{}
	}
	raw, _ := json.Marshal(rules)
	if err := s.Repo.SetBucketField(ctx, b.Name, "lifecycle", string(raw)); err != nil {
		return nil, err
	}
	b.Lifecycle = raw
	s.RecordEvent(ctx, b.AccountID, b.Name, "bucket", "lifecycle_updated", "Lifecycle configuration of "+b.Name+" updated", map[string]any{"rules": len(rules)})
	return map[string]any{"rules": rules}, nil
}

// LifecycleAction is one action returned by the engine (engine-protocol §9).
type LifecycleAction struct {
	Type         string  `json:"type"`
	Key          string  `json:"key"`
	VersionID    string  `json:"version_id"`
	UploadID     *string `json:"upload_id"`
	StorageClass string  `json:"storage_class,omitempty"`
	RuleID       string  `json:"rule_id"`
	Applied      bool    `json:"applied"`
	Note         string  `json:"note,omitempty"`
}

type LifecycleRun struct {
	Bucket           string            `json:"bucket"`
	EvaluatedObjects int               `json:"evaluated_objects"`
	EvaluatedUploads int               `json:"evaluated_uploads"`
	Applied          int               `json:"applied"`
	Actions          []LifecycleAction `json:"actions"`
}

const lifecycleMaxObjects = 100000

// RunLifecycle asks the engine which lifecycle actions are due and applies them.
func (s *S3Service) RunLifecycle(ctx context.Context, b *repositories.S3Bucket) (*LifecycleRun, error) {
	out := &LifecycleRun{Bucket: b.Name, Actions: []LifecycleAction{}}
	var enabled []LifecycleRule
	for _, r := range rulesOf(b) {
		if r.Status == "Enabled" {
			enabled = append(enabled, r)
		}
	}
	if len(enabled) == 0 {
		return out, nil
	}
	vers, err := s.Repo.AllVersions(ctx, b.Name, lifecycleMaxObjects)
	if err != nil {
		return nil, err
	}
	ups, err := s.Repo.ListUploads(ctx, b.Name)
	if err != nil {
		return nil, err
	}
	objs := make([]map[string]any, 0, len(vers))
	for _, v := range vers {
		if v.DeleteMarker {
			continue
		}
		objs = append(objs, map[string]any{"key": v.Key, "version_id": v.VersionID, "is_latest": v.IsLatest, "last_modified": v.LastModified,
			"size": v.Size, "storage_class": v.StorageClass, "noncurrent_since": v.NoncurrentSince})
	}
	mpus := make([]map[string]any, 0, len(ups))
	for _, u := range ups {
		mpus = append(mpus, map[string]any{"upload_id": u.UploadID, "key": u.Key, "initiated": u.Initiated})
	}
	out.EvaluatedObjects, out.EvaluatedUploads = len(objs), len(mpus)
	req := map[string]any{"now": nowRFC3339(), "rules": enabled, "objects": objs, "multipart_uploads": mpus}
	var resp struct {
		Actions []LifecycleAction `json:"actions"`
	}
	if err := s.Engine.Raw(ctx, "POST", "/v1/storage/lifecycle/evaluate", req, &resp); err != nil {
		return nil, EngineErr(err)
	}
	for _, a := range resp.Actions {
		a.Applied, a.Note = s.applyLifecycle(ctx, b, a)
		if a.Applied {
			out.Applied++
		}
		out.Actions = append(out.Actions, a)
	}
	if out.Applied > 0 {
		detail := out.Actions
		if len(detail) > 100 {
			detail = detail[:100]
		}
		counts := map[string]int{}
		for _, a := range out.Actions {
			if a.Applied {
				counts[a.Type]++
			}
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+strconv.Itoa(counts[k]))
		}
		s.RecordEvent(ctx, b.AccountID, b.Name, "bucket", "lifecycle_applied",
			"Lifecycle applied to "+b.Name+": "+strings.Join(parts, ", "), map[string]any{"actions": detail})
	}
	return out, nil
}

func (s *S3Service) applyLifecycle(ctx context.Context, b *repositories.S3Bucket, a LifecycleAction) (bool, string) {
	switch a.Type {
	case "expire":
		cur, err := s.Repo.GetLatest(ctx, b.Name, a.Key)
		if err != nil || cur.DeleteMarker {
			return false, "object no longer current"
		}
		if a.VersionID != "" && cur.VersionID != a.VersionID {
			return false, "a newer version exists"
		}
		if _, err := s.DeleteObject(ctx, b, a.Key, ""); err != nil {
			return false, err.Error()
		}
		return true, ""
	case "expire_noncurrent":
		v, err := s.Repo.GetVersion(ctx, b.Name, a.Key, a.VersionID)
		if err != nil || v.IsLatest {
			return false, "version missing or current"
		}
		if _, err := s.DeleteObject(ctx, b, a.Key, a.VersionID); err != nil {
			return false, err.Error()
		}
		return true, ""
	case "transition":
		if !ValidStorageClasses[a.StorageClass] {
			return false, "invalid storage class"
		}
		vid := a.VersionID
		if vid == "" {
			cur, err := s.Repo.GetLatest(ctx, b.Name, a.Key)
			if err != nil {
				return false, "object missing"
			}
			vid = cur.VersionID
		}
		if err := s.Repo.SetStorageClass(ctx, b.Name, a.Key, vid, a.StorageClass); err != nil {
			return false, "version missing"
		}
		return true, ""
	case "abort_multipart":
		if a.UploadID == nil {
			return false, "missing upload_id"
		}
		if err := s.AbortUpload(ctx, b, *a.UploadID); err != nil {
			return false, err.Error()
		}
		return true, ""
	}
	return false, "unknown action type"
}

// LifecycleSweep evaluates every bucket with enabled lifecycle rules (background worker).
func (s *S3Service) LifecycleSweep(ctx context.Context) {
	bs, err := s.Repo.AllBuckets(ctx)
	if err != nil {
		s.Log.Error("lifecycle sweep", "err", err)
		return
	}
	for i := range bs {
		if ctx.Err() != nil {
			return
		}
		has := false
		for _, r := range rulesOf(&bs[i]) {
			has = has || r.Status == "Enabled"
		}
		if !has {
			continue
		}
		if _, err := s.RunLifecycle(ctx, &bs[i]); err != nil {
			s.Log.Warn("lifecycle run failed", "bucket", bs[i].Name, "err", err)
		}
	}
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/orchestration"
	"cloudsim/backend/internal/policy"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/services"
)

// S3 routes. Bucket and object operations are registered with action "" (authentication only) and
// authorized inside the handler by s3Access, which evaluates identity policies together with the
// bucket policy (resource policy). That way an explicit Deny in a bucket policy blocks IAM-allowed
// principals and an Allow in a bucket policy grants access to same-account IAM users that have no
// identity policy.
func init() {
	RegisterModule("s3", 30, func(s *Server) {
		svc := services.NewS3Service(s.Deps, repositories.NewS3Repo(s.Store.DB), s.Cfg.ObjectStorageDir, s.Cfg.MaxObjectSize)
		s.Ext.Store("s3", svc)
		s.Resyncers = append(s.Resyncers, svc.Resync)
		s.Workers = append(s.Workers, func(ctx context.Context) {
			orchestration.Every(ctx, s.Cfg.LifecycleInterval, svc.LifecycleSweep)
		})
		s.DashboardContrib = append(s.DashboardContrib, func(ctx context.Context, account string, out map[string]any) {
			st, err := svc.Stats(ctx, account)
			if err != nil {
				return
			}
			out["buckets"], out["objects"], out["storage_bytes"] = st.Buckets, st.Objects, st.SizeBytes
			if n, ok := out["resources_total"].(int); ok {
				out["resources_total"] = n + st.Buckets
			}
		})
		s.routesS3(svc)
	})
}

// policyAdminActions are never blocked for the account root by a bucket policy Deny, so the owner
// can always repair a policy that locks everyone out (as in AWS).
var policyAdminActions = map[string]bool{"s3:GetBucketPolicy": true, "s3:PutBucketPolicy": true, "s3:DeleteBucketPolicy": true}

// s3Access resolves the bucket and authorizes action on it (objectKey != nil → object ARN).
func (s *Server) s3Access(c *Ctx, svc *services.S3Service, bucket, action string, objectKey *string, extra map[string]string) (*repositories.S3Bucket, error) {
	b, err := svc.Lookup(c.Context(), bucket)
	if err != nil {
		return nil, err
	}
	arn := services.BucketARN(bucket)
	if objectKey != nil {
		arn = services.ObjectARN(bucket, *objectKey)
	}
	publicRead := b.ACL == "public-read" && action == "s3:GetObject"
	if b.AccountID != c.P.AccountID {
		// Simplification: acl public-read lets any authenticated platform principal read objects.
		if publicRead {
			return b, nil
		}
		return nil, apierr.New(404, "NotFound", "the specified bucket %s does not exist", bucket)
	}
	rp := svc.PolicyFor(b)
	ai, _ := c.R.Context().Value(keyAudit).(*auditInfo)
	if ai != nil && ai.action == "" {
		ai.action, ai.resource = action, arn
	}
	deny := func(d policy.Decision) error {
		if ai != nil {
			ai.action, ai.resource, ai.denied, ai.detail = action, arn, true, string(d)
		}
		return apierr.Denied("User: %s is not authorized to perform: %s on resource: %s (%s)", c.P.ARN, action, arn, d)
	}
	ctxKeys := map[string]string{}
	for k, v := range extra {
		ctxKeys[k] = v
	}
	if c.P.Kind == auth.KindRoot {
		// Root is allowed by identity, but an explicit Deny in the bucket policy still applies.
		if len(rp) > 0 && !policyAdminActions[action] {
			res := policy.Evaluate(policy.Request{PrincipalARN: c.P.ARN, AccountID: c.P.AccountID, Action: action, Resource: arn, Context: ctxKeys}, nil, rp)
			if res.Decision == policy.ExplicitDeny {
				return nil, deny(res.Decision)
			}
		}
		return b, nil
	}
	res, err := s.Auth.Authorize(c.Context(), c.P, action, arn, rp, ctxKeys)
	if err != nil {
		return nil, err
	}
	if res.Allowed() || (publicRead && res.Decision == policy.ImplicitDeny) {
		return b, nil
	}
	return nil, deny(res.Decision)
}

func s3Meta(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-meta-") && len(v) > 0 {
			out[strings.TrimPrefix(lk, "x-amz-meta-")] = v[0]
		}
	}
	return out
}

func s3ObjectHeaders(w http.ResponseWriter, o *repositories.S3Object) {
	h := w.Header()
	h.Set("x-amz-version-id", o.VersionID)
	if o.DeleteMarker {
		h.Set("x-amz-delete-marker", "true")
		return
	}
	h.Set("ETag", `"`+o.ETag+`"`)
	h.Set("Content-Type", o.ContentType)
	h.Set("x-amz-storage-class", o.StorageClass)
	for k, v := range o.Metadata {
		h.Set("x-amz-meta-"+k, v)
	}
	// User content is served from the API origin: never let it execute as an active document.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
}

func (s *Server) routesS3(svc *services.S3Service) {
	const p = "/api/v1/s3"
	acct := func(c *Ctx) string { return c.P.AccountID }
	keyOf := func(c *Ctx) *string { k := c.Path("key"); return &k }
	pageOf := func(c *Ctx) (int, int) {
		lp := parseList(c.R)
		return lp.Page, lp.PageSize
	}

	s.op("GET "+p+"/buckets", "s3:ListAllMyBuckets", func(*Ctx) string { return "arn:cloudsim:s3:::*" }, func(c *Ctx) error {
		l, err := svc.ListBuckets(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "versioning", "acl", "has_policy"))
	})
	s.op("GET "+p+"/stats", "s3:ListAllMyBuckets", func(*Ctx) string { return "arn:cloudsim:s3:::*" }, func(c *Ctx) error {
		st, err := svc.Stats(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, st)
	})
	s.op("POST "+p+"/buckets", "", nil, func(c *Ctx) error {
		var in services.CreateBucketInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := c.Authorize("s3:CreateBucket", services.BucketARN(in.Name), nil, nil); err != nil {
			return err
		}
		v, err := svc.CreateBucket(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, v)
	})
	s.op("GET "+p+"/buckets/{bucket}", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:ListBucket", nil, nil)
		if err != nil {
			return err
		}
		v, err := svc.View(c.Context(), b)
		if err != nil {
			return err
		}
		return c.JSON(200, v)
	})
	s.op("DELETE "+p+"/buckets/{bucket}", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:DeleteBucket", nil, nil)
		if err != nil {
			return err
		}
		if err := svc.DeleteBucket(c.Context(), b, c.Query("force") == "true"); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("PUT "+p+"/buckets/{bucket}/versioning", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:PutBucketVersioning", nil, nil)
		if err != nil {
			return err
		}
		var in struct {
			Status string `json:"status"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		v, err := svc.SetVersioning(c.Context(), b, in.Status)
		if err != nil {
			return err
		}
		return c.JSON(200, v)
	})
	s.op("PUT "+p+"/buckets/{bucket}/acl", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:PutBucketAcl", nil, nil)
		if err != nil {
			return err
		}
		var in struct {
			ACL string `json:"acl"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		v, err := svc.SetACL(c.Context(), b, in.ACL)
		if err != nil {
			return err
		}
		return c.JSON(200, v)
	})

	// Bucket policy
	s.op("GET "+p+"/buckets/{bucket}/policy", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:GetBucketPolicy", nil, nil)
		if err != nil {
			return err
		}
		doc, err := svc.GetPolicy(b)
		if err != nil {
			return err
		}
		return c.JSON(200, doc)
	})
	s.op("PUT "+p+"/buckets/{bucket}/policy", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:PutBucketPolicy", nil, nil)
		if err != nil {
			return err
		}
		doc, err := decodeBucketPolicy(c)
		if err != nil {
			return err
		}
		out, err := svc.PutPolicy(c.Context(), b, doc)
		if err != nil {
			return err
		}
		return c.JSON(200, out)
	})
	s.op("DELETE "+p+"/buckets/{bucket}/policy", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:DeleteBucketPolicy", nil, nil)
		if err != nil {
			return err
		}
		if err := svc.DeletePolicy(c.Context(), b); err != nil {
			return err
		}
		return c.NoContent()
	})

	// Lifecycle
	s.op("GET "+p+"/buckets/{bucket}/lifecycle", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:GetLifecycleConfiguration", nil, nil)
		if err != nil {
			return err
		}
		return c.JSON(200, svc.GetLifecycle(b))
	})
	s.op("PUT "+p+"/buckets/{bucket}/lifecycle", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:PutLifecycleConfiguration", nil, nil)
		if err != nil {
			return err
		}
		var in struct {
			Rules []services.LifecycleRule `json:"rules"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		out, err := svc.PutLifecycle(c.Context(), b, in.Rules)
		if err != nil {
			return err
		}
		return c.JSON(200, out)
	})
	s.op("DELETE "+p+"/buckets/{bucket}/lifecycle", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:PutLifecycleConfiguration", nil, nil)
		if err != nil {
			return err
		}
		if _, err := svc.PutLifecycle(c.Context(), b, nil); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/buckets/{bucket}/lifecycle/run", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:PutLifecycleConfiguration", nil, nil)
		if err != nil {
			return err
		}
		out, err := svc.RunLifecycle(c.Context(), b)
		if err != nil {
			return err
		}
		return c.JSON(200, out)
	})

	// Listing
	s.op("GET "+p+"/buckets/{bucket}/objects", "", nil, func(c *Ctx) error {
		prefix, delim := c.Query("prefix"), c.Query("delimiter")
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:ListBucket", nil, map[string]string{"s3:prefix": prefix, "s3:delimiter": delim})
		if err != nil {
			return err
		}
		page, size := pageOf(c)
		l, err := svc.ListObjects(c.Context(), b, prefix, delim, c.Query("q"), page, size)
		if err != nil {
			return err
		}
		return c.JSON(200, l)
	})
	s.op("GET "+p+"/buckets/{bucket}/versions", "", nil, func(c *Ctx) error {
		prefix := c.Query("prefix")
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:ListBucketVersions", nil, map[string]string{"s3:prefix": prefix})
		if err != nil {
			return err
		}
		page, size := pageOf(c)
		l, err := svc.ListVersions(c.Context(), b, prefix, page, size)
		if err != nil {
			return err
		}
		return c.JSON(200, l)
	})

	// Objects
	s.op("PUT "+p+"/buckets/{bucket}/objects/{key...}", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:PutObject", keyOf(c), nil)
		if err != nil {
			return err
		}
		if c.R.ContentLength > s.Cfg.MaxObjectSize {
			return apierr.New(http.StatusRequestEntityTooLarge, "EntityTooLarge", "object exceeds the maximum allowed size of %d bytes", s.Cfg.MaxObjectSize)
		}
		body := http.MaxBytesReader(c.W, c.R.Body, s.Cfg.MaxObjectSize+1)
		o, err := svc.PutObject(c.Context(), b, services.PutObjectInput{Key: c.Path("key"), ContentType: c.R.Header.Get("Content-Type"),
			StorageClass: c.R.Header.Get("x-amz-storage-class"), Metadata: s3Meta(c.R.Header), Body: body})
		if err != nil {
			return err
		}
		c.W.Header().Set("ETag", `"`+o.ETag+`"`)
		c.W.Header().Set("x-amz-version-id", o.VersionID)
		return c.JSON(200, o)
	})
	s.op("POST "+p+"/buckets/{bucket}/upload", "", nil, func(c *Ctx) error {
		return s.s3FormUpload(c, svc)
	})
	s.op("GET "+p+"/buckets/{bucket}/objects/{key...}", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:GetObject", keyOf(c), nil)
		if err != nil {
			return err
		}
		o, err := svc.GetObject(c.Context(), b, c.Path("key"), c.Query("version_id"))
		if err != nil {
			if o != nil {
				s3ObjectHeaders(c.W, o)
			}
			return err
		}
		f, err := svc.Open(o)
		if err != nil {
			return err
		}
		defer f.Close()
		s3ObjectHeaders(c.W, o)
		http.ServeContent(c.W, c.R, "", db.Parse(o.LastModified), f)
		return nil
	})
	s.op("GET "+p+"/buckets/{bucket}/object-info/{key...}", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:GetObject", keyOf(c), nil)
		if err != nil {
			return err
		}
		o, err := svc.GetObject(c.Context(), b, c.Path("key"), c.Query("version_id"))
		if err != nil {
			return err
		}
		return c.JSON(200, o)
	})
	s.op("DELETE "+p+"/buckets/{bucket}/objects/{key...}", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:DeleteObject", keyOf(c), nil)
		if err != nil {
			return err
		}
		res, err := svc.DeleteObject(c.Context(), b, c.Path("key"), c.Query("version_id"))
		if err != nil {
			return err
		}
		if res.VersionID != "" {
			c.W.Header().Set("x-amz-version-id", res.VersionID)
		}
		if res.DeleteMarker {
			c.W.Header().Set("x-amz-delete-marker", "true")
		}
		return c.NoContent()
	})

	// Multipart uploads
	s.op("GET "+p+"/buckets/{bucket}/multipart", "", nil, func(c *Ctx) error {
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:ListBucketMultipartUploads", nil, nil)
		if err != nil {
			return err
		}
		l, err := svc.ListUploads(c.Context(), b)
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "key"))
	})
	s.op("POST "+p+"/buckets/{bucket}/multipart", "", nil, func(c *Ctx) error {
		var in services.InitiateInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		b, err := s.s3Access(c, svc, c.Path("bucket"), "s3:PutObject", &in.Key, nil)
		if err != nil {
			return err
		}
		u, err := svc.InitiateUpload(c.Context(), b, in)
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"upload_id": u.UploadID, "bucket": u.Bucket, "key": u.Key})
	})
	// upload resolves the bucket + upload and authorizes action on the upload's object key.
	upload := func(c *Ctx, action string) (*repositories.S3Bucket, *repositories.S3Upload, error) {
		b, err := svc.Lookup(c.Context(), c.Path("bucket"))
		if err != nil {
			return nil, nil, err
		}
		if b.AccountID != c.P.AccountID {
			return nil, nil, apierr.New(404, "NotFound", "the specified bucket %s does not exist", b.Name)
		}
		u, err := svc.GetUpload(c.Context(), b, c.Path("upload_id"))
		if err != nil {
			return nil, nil, err
		}
		if b, err = s.s3Access(c, svc, b.Name, action, &u.Key, nil); err != nil {
			return nil, nil, err
		}
		return b, u, nil
	}
	s.op("PUT "+p+"/buckets/{bucket}/multipart/{upload_id}/parts/{n}", "", nil, func(c *Ctx) error {
		b, u, err := upload(c, "s3:PutObject")
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(c.Path("n"))
		if err != nil {
			return apierr.Validation("part number must be an integer between 1 and 10000")
		}
		if c.R.ContentLength > s.Cfg.MaxObjectSize {
			return apierr.New(http.StatusRequestEntityTooLarge, "EntityTooLarge", "part exceeds the maximum allowed size of %d bytes", s.Cfg.MaxObjectSize)
		}
		part, err := svc.UploadPart(c.Context(), b, u, n, http.MaxBytesReader(c.W, c.R.Body, s.Cfg.MaxObjectSize+1))
		if err != nil {
			return err
		}
		c.W.Header().Set("ETag", `"`+part.ETag+`"`)
		return c.JSON(200, part)
	})
	s.op("POST "+p+"/buckets/{bucket}/multipart/{upload_id}/complete", "", nil, func(c *Ctx) error {
		b, u, err := upload(c, "s3:PutObject")
		if err != nil {
			return err
		}
		var in struct {
			Parts []services.CompletedPart `json:"parts"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		o, err := svc.CompleteUpload(c.Context(), b, u, in.Parts)
		if err != nil {
			return err
		}
		c.W.Header().Set("x-amz-version-id", o.VersionID)
		return c.JSON(200, o)
	})
	s.op("DELETE "+p+"/buckets/{bucket}/multipart/{upload_id}", "", nil, func(c *Ctx) error {
		b, u, err := upload(c, "s3:AbortMultipartUpload")
		if err != nil {
			return err
		}
		if err := svc.AbortUpload(c.Context(), b, u.UploadID); err != nil {
			return err
		}
		return c.NoContent()
	})
}

// decodeBucketPolicy accepts the bare document, {"policy": doc} or {"policy": "<json string>"}.
func decodeBucketPolicy(c *Ctx) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.Decode(&raw); err != nil {
		return nil, err
	}
	var wrapper struct {
		Policy         json.RawMessage `json:"policy"`
		PolicyDocument json.RawMessage `json:"policy_document"`
	}
	if json.Unmarshal(raw, &wrapper) == nil {
		inner := wrapper.Policy
		if len(inner) == 0 {
			inner = wrapper.PolicyDocument
		}
		if len(inner) > 0 {
			var str string
			if json.Unmarshal(inner, &str) == nil {
				return json.RawMessage(str), nil
			}
			return inner, nil
		}
	}
	return raw, nil
}

// s3FormUpload handles console uploads: multipart/form-data with optional "prefix" (must precede
// the files) and one or more "file" parts, each streamed straight to blob storage.
func (s *Server) s3FormUpload(c *Ctx, svc *services.S3Service) error {
	bucket := c.Path("bucket")
	if _, err := svc.GetBucket(c.Context(), c.P.AccountID, bucket); err != nil {
		return err
	}
	c.R.Body = http.MaxBytesReader(c.W, c.R.Body, 10*s.Cfg.MaxObjectSize+(1<<20))
	mr, err := c.R.MultipartReader()
	if err != nil {
		return apierr.Validation("expected multipart/form-data body: %v", err)
	}
	prefix := ""
	objs := []*repositories.S3Object{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return apierr.New(http.StatusRequestEntityTooLarge, "EntityTooLarge", "upload too large")
			}
			return apierr.Validation("malformed multipart body: %v", err)
		}
		switch part.FormName() {
		case "prefix":
			v, _ := io.ReadAll(io.LimitReader(part, 1025))
			prefix = string(v)
		case "file":
			name := part.FileName()
			if name == "" {
				part.Close()
				return apierr.Validation("file parts must have a filename")
			}
			key := prefix + name
			b, err := s.s3Access(c, svc, bucket, "s3:PutObject", &key, nil)
			if err != nil {
				part.Close()
				return err
			}
			ct := part.Header.Get("Content-Type")
			o, err := svc.PutObject(c.Context(), b, services.PutObjectInput{Key: key, ContentType: ct, Metadata: map[string]string{}, Body: part})
			part.Close()
			if err != nil {
				return err
			}
			objs = append(objs, o)
		default:
			part.Close()
		}
	}
	if len(objs) == 0 {
		return apierr.Validation("no file parts in upload")
	}
	return c.JSON(200, map[string]any{"objects": objs})
}

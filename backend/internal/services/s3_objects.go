package services

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// ---- blob storage
//
// Bytes are stored at <Dir>/<bucket>/<blob id>, where blob id is 32 random hex chars. User-supplied
// keys are never used as file system paths, so keys like "../../etc/passwd" are just strings.

var blobIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s *S3Service) bucketDir(bucket string) (string, error) {
	if ValidateBucketName(bucket) != nil {
		return "", errors.New("invalid bucket name for storage path")
	}
	return filepath.Join(s.Dir, bucket), nil
}

func (s *S3Service) blobPath(bucket, blob string) (string, error) {
	d, err := s.bucketDir(bucket)
	if err != nil {
		return "", err
	}
	if !blobIDRe.MatchString(blob) {
		return "", errors.New("invalid blob id")
	}
	return filepath.Join(d, blob), nil
}

func (s *S3Service) ensureBucketDir(bucket string) error {
	d, err := s.bucketDir(bucket)
	if err != nil {
		return err
	}
	return os.MkdirAll(d, 0o750)
}

func (s *S3Service) removeBucketDir(bucket string) error {
	d, err := s.bucketDir(bucket)
	if err != nil {
		return err
	}
	return os.RemoveAll(d)
}

// BlobExists reports whether a blob file is present (used by tests/diagnostics).
func (s *S3Service) BlobExists(bucket, blob string) bool {
	p, err := s.blobPath(bucket, blob)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func (s *S3Service) removeBlobs(bucket string, blobs []string) {
	for _, b := range blobs {
		if b == "" {
			continue
		}
		p, err := s.blobPath(bucket, b)
		if err != nil {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			s.Log.Warn("remove blob", "bucket", bucket, "blob", b, "err", err)
		}
	}
}

func tooLarge(limit int64) error {
	return apierr.New(http.StatusRequestEntityTooLarge, "EntityTooLarge", "object exceeds the maximum allowed size of %d bytes", limit)
}

// writeBlob streams r into a new blob (temp file + fsync + rename), computing the MD5 on the fly.
func (s *S3Service) writeBlob(bucket string, r io.Reader, limit int64) (blob string, size int64, sum []byte, err error) {
	if err := s.ensureBucketDir(bucket); err != nil {
		return "", 0, nil, err
	}
	dir, _ := s.bucketDir(bucket)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", 0, nil, err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return "", 0, nil, tooLarge(limit)
		}
		return "", 0, nil, apierr.Validation("failed to read request body: %v", err)
	}
	if n > limit {
		return "", 0, nil, tooLarge(limit)
	}
	if err = f.Sync(); err != nil {
		return "", 0, nil, err
	}
	if err = f.Close(); err != nil {
		return "", 0, nil, err
	}
	blob = ids.Token()[:32]
	final, _ := s.blobPath(bucket, blob)
	if err = os.Rename(tmp, final); err != nil {
		return "", 0, nil, err
	}
	return blob, n, h.Sum(nil), nil
}

// Open opens an object version's bytes.
func (s *S3Service) Open(o *repositories.S3Object) (*os.File, error) {
	p, err := s.blobPath(o.Bucket, o.Blob)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// ---- objects

type PutObjectInput struct {
	Key          string
	ContentType  string
	StorageClass string
	Metadata     map[string]string
	Body         io.Reader
}

func (s *S3Service) newVersionID(b *repositories.S3Bucket) (string, bool) {
	if b.Versioning == "Enabled" {
		return ids.Token()[:32], false
	}
	return "null", true
}

func (s *S3Service) PutObject(ctx context.Context, b *repositories.S3Bucket, in PutObjectInput) (*repositories.S3Object, error) {
	if err := ValidateKey(in.Key); err != nil {
		return nil, err
	}
	if in.ContentType == "" {
		in.ContentType = "application/octet-stream"
	}
	if err := validateObjectAttrs(in.ContentType, in.Metadata, in.StorageClass); err != nil {
		return nil, err
	}
	blob, size, sum, err := s.writeBlob(b.Name, in.Body, s.MaxSize)
	if err != nil {
		return nil, err
	}
	return s.storeVersion(ctx, b, &repositories.S3Object{Bucket: b.Name, Key: in.Key, Size: size, ETag: hex.EncodeToString(sum),
		ContentType: in.ContentType, StorageClass: in.StorageClass, Metadata: in.Metadata, Blob: blob}, "")
}

// storeVersion records obj (whose blob is already written) as the latest version, honoring the
// bucket's versioning state. uploadID != "" completes that multipart upload in the same transaction.
func (s *S3Service) storeVersion(ctx context.Context, b *repositories.S3Bucket, obj *repositories.S3Object, uploadID string) (*repositories.S3Object, error) {
	if obj.Metadata == nil {
		obj.Metadata = map[string]string{}
	}
	if obj.StorageClass == "" {
		obj.StorageClass = "STANDARD"
	}
	var replaceNull bool
	obj.VersionID, replaceNull = s.newVersionID(b)
	var freed []string
	var err error
	if uploadID != "" {
		freed, err = s.Repo.CompleteUpload(ctx, uploadID, obj, replaceNull)
	} else {
		freed, err = s.Repo.PutVersion(ctx, obj, replaceNull)
	}
	if err != nil {
		s.removeBlobs(b.Name, []string{obj.Blob})
		if errors.Is(err, repositories.ErrNotFound) {
			return nil, apierr.New(404, "NotFound", "the specified multipart upload does not exist")
		}
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return nil, noSuchBucket(b.Name)
		}
		return nil, err
	}
	s.removeBlobs(b.Name, freed)
	return obj, nil
}

// GetObject returns the latest version (or a specific one). A delete marker is returned together
// with a 404 error so callers can emit x-amz-delete-marker.
func (s *S3Service) GetObject(ctx context.Context, b *repositories.S3Bucket, key, versionID string) (*repositories.S3Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	var o *repositories.S3Object
	var err error
	if versionID != "" {
		o, err = s.Repo.GetVersion(ctx, b.Name, key, versionID)
	} else {
		o, err = s.Repo.GetLatest(ctx, b.Name, key)
	}
	if errors.Is(err, repositories.ErrNotFound) {
		if versionID != "" {
			return nil, apierr.New(404, "NotFound", "the specified version %s of key %s does not exist", versionID, key)
		}
		return nil, noSuchKey(key)
	}
	if err != nil {
		return nil, err
	}
	if o.DeleteMarker {
		if versionID != "" {
			return o, apierr.New(405, "MethodNotAllowed", "the specified version is a delete marker")
		}
		return o, noSuchKey(key)
	}
	return o, nil
}

// DeleteResult describes the outcome of DeleteObject.
type DeleteResult struct {
	Key          string `json:"key"`
	VersionID    string `json:"version_id,omitempty"`
	DeleteMarker bool   `json:"delete_marker"`
}

func (s *S3Service) DeleteObject(ctx context.Context, b *repositories.S3Bucket, key, versionID string) (*DeleteResult, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if versionID != "" || b.Versioning == "Disabled" {
		vid := versionID
		if vid == "" {
			vid = "null"
		}
		o, err := s.Repo.DeleteVersion(ctx, b.Name, key, vid)
		if errors.Is(err, repositories.ErrNotFound) {
			if versionID == "" {
				return &DeleteResult{Key: key}, nil // deleting a missing key is a no-op, like S3
			}
			return nil, apierr.New(404, "NotFound", "the specified version %s of key %s does not exist", versionID, key)
		}
		if err != nil {
			return nil, err
		}
		s.removeBlobs(b.Name, []string{o.Blob})
		return &DeleteResult{Key: key, VersionID: versionID, DeleteMarker: o.DeleteMarker}, nil
	}
	// Enabled → new delete marker; Suspended → 'null' delete marker replacing the null version.
	marker := &repositories.S3Object{Bucket: b.Name, Key: key, DeleteMarker: true}
	if _, err := s.storeVersion(ctx, b, marker, ""); err != nil {
		return nil, err
	}
	return &DeleteResult{Key: key, VersionID: marker.VersionID, DeleteMarker: true}, nil
}

// ---- multipart uploads

type InitiateInput struct {
	Key          string            `json:"key"`
	ContentType  string            `json:"content_type"`
	StorageClass string            `json:"storage_class"`
	Metadata     map[string]string `json:"metadata"`
}

func (s *S3Service) InitiateUpload(ctx context.Context, b *repositories.S3Bucket, in InitiateInput) (*repositories.S3Upload, error) {
	if err := ValidateKey(in.Key); err != nil {
		return nil, err
	}
	if in.ContentType == "" {
		in.ContentType = "application/octet-stream"
	}
	if err := validateObjectAttrs(in.ContentType, in.Metadata, in.StorageClass); err != nil {
		return nil, err
	}
	if in.Metadata == nil {
		in.Metadata = map[string]string{}
	}
	if in.StorageClass != "" && in.StorageClass != "STANDARD" {
		in.Metadata["x-cloudsim-storage-class"] = in.StorageClass
	}
	u := &repositories.S3Upload{UploadID: ids.Token()[:32], Bucket: b.Name, Key: in.Key, ContentType: in.ContentType, Metadata: in.Metadata}
	if err := s.Repo.InsertUpload(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func noSuchUpload(id string) error {
	return apierr.New(404, "NotFound", "the specified multipart upload %s does not exist", id)
}

func (s *S3Service) GetUpload(ctx context.Context, b *repositories.S3Bucket, uploadID string) (*repositories.S3Upload, error) {
	u, err := s.Repo.GetUpload(ctx, b.Name, uploadID)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, noSuchUpload(uploadID)
	}
	return u, err
}

func (s *S3Service) ListUploads(ctx context.Context, b *repositories.S3Bucket) ([]repositories.S3Upload, error) {
	return s.Repo.ListUploads(ctx, b.Name)
}

func (s *S3Service) UploadPart(ctx context.Context, b *repositories.S3Bucket, u *repositories.S3Upload, n int, body io.Reader) (*repositories.S3Part, error) {
	if n < 1 || n > 10000 {
		return nil, apierr.Validation("part number must be between 1 and 10000")
	}
	blob, size, sum, err := s.writeBlob(b.Name, body, s.MaxSize)
	if err != nil {
		return nil, err
	}
	p := &repositories.S3Part{UploadID: u.UploadID, PartNumber: n, Size: size, ETag: hex.EncodeToString(sum), Blob: blob}
	old, err := s.Repo.PutPart(ctx, p)
	if err != nil {
		s.removeBlobs(b.Name, []string{blob})
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return nil, noSuchUpload(u.UploadID)
		}
		return nil, err
	}
	s.removeBlobs(b.Name, []string{old})
	return p, nil
}

type CompletedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

func (s *S3Service) CompleteUpload(ctx context.Context, b *repositories.S3Bucket, u *repositories.S3Upload, parts []CompletedPart) (*repositories.S3Object, error) {
	if len(parts) == 0 {
		return nil, apierr.New(400, "InvalidRequest", "you must specify at least one part")
	}
	stored, err := s.Repo.ListParts(ctx, u.UploadID)
	if err != nil {
		return nil, err
	}
	byNum := map[int]repositories.S3Part{}
	for _, p := range stored {
		byNum[p.PartNumber] = p
	}
	var total int64
	md5s := make([]byte, 0, len(parts)*md5.Size)
	use := make([]repositories.S3Part, 0, len(parts))
	for i, cp := range parts {
		if i > 0 && cp.PartNumber <= parts[i-1].PartNumber {
			return nil, apierr.New(400, "InvalidPartOrder", "the list of parts was not in ascending order")
		}
		sp, ok := byNum[cp.PartNumber]
		if !ok || !strings.EqualFold(strings.Trim(cp.ETag, `"`), sp.ETag) {
			return nil, apierr.New(400, "InvalidPart", "part %d could not be found or its etag does not match", cp.PartNumber)
		}
		raw, err := hex.DecodeString(sp.ETag)
		if err != nil {
			return nil, apierr.New(400, "InvalidPart", "part %d has an invalid etag", cp.PartNumber)
		}
		md5s = append(md5s, raw...)
		total += sp.Size
		use = append(use, sp)
	}
	if total > s.MaxSize {
		return nil, tooLarge(s.MaxSize)
	}
	readers := make([]io.Reader, 0, len(use))
	for _, p := range use {
		path, err := s.blobPath(b.Name, p.Blob)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		readers = append(readers, f)
	}
	blob, size, _, err := s.writeBlob(b.Name, io.MultiReader(readers...), s.MaxSize)
	if err != nil {
		return nil, err
	}
	sum := md5.Sum(md5s)
	meta := map[string]string{}
	class := ""
	for k, v := range u.Metadata {
		if k == "x-cloudsim-storage-class" {
			class = v
			continue
		}
		meta[k] = v
	}
	obj := &repositories.S3Object{Bucket: b.Name, Key: u.Key, Size: size, ETag: hex.EncodeToString(sum[:]) + "-" + strconv.Itoa(len(use)),
		ContentType: u.ContentType, StorageClass: class, Metadata: meta, Blob: blob}
	return s.storeVersion(ctx, b, obj, u.UploadID)
}

func (s *S3Service) AbortUpload(ctx context.Context, b *repositories.S3Bucket, uploadID string) error {
	if _, err := s.GetUpload(ctx, b, uploadID); err != nil {
		return err
	}
	blobs, err := s.Repo.DeleteUpload(ctx, uploadID)
	if errors.Is(err, repositories.ErrNotFound) {
		return noSuchUpload(uploadID)
	}
	if err != nil {
		return err
	}
	s.removeBlobs(b.Name, blobs)
	return nil
}

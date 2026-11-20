package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"cloudsim/backend/internal/db"
)

// S3Bucket is a bucket row. Usage fields are filled by queries that aggregate objects.
type S3Bucket struct {
	Name       string          `json:"name"`
	AccountID  string          `json:"-"`
	ACL        string          `json:"acl"`
	Versioning string          `json:"versioning"`
	Policy     string          `json:"-"`
	Lifecycle  json.RawMessage `json:"-"`
	CreatedAt  string          `json:"created_at"`
}

// S3Object is one object version (or delete marker).
type S3Object struct {
	ID              int64             `json:"-"`
	Bucket          string            `json:"-"`
	Key             string            `json:"key"`
	VersionID       string            `json:"version_id"`
	IsLatest        bool              `json:"is_latest"`
	DeleteMarker    bool              `json:"delete_marker"`
	Size            int64             `json:"size"`
	ETag            string            `json:"etag"`
	ContentType     string            `json:"content_type"`
	StorageClass    string            `json:"storage_class"`
	Metadata        map[string]string `json:"metadata"`
	Blob            string            `json:"-"`
	LastModified    string            `json:"last_modified"`
	NoncurrentSince *string           `json:"-"`
}

// S3Upload is an in-progress multipart upload.
type S3Upload struct {
	UploadID    string            `json:"upload_id"`
	Bucket      string            `json:"bucket"`
	Key         string            `json:"key"`
	ContentType string            `json:"content_type"`
	Metadata    map[string]string `json:"metadata"`
	Initiated   string            `json:"initiated"`
	Parts       int               `json:"parts"`
	SizeBytes   int64             `json:"size_bytes"`
}

// S3Part is an uploaded part of a multipart upload.
type S3Part struct {
	UploadID     string `json:"-"`
	PartNumber   int    `json:"part_number"`
	Size         int64  `json:"size"`
	ETag         string `json:"etag"`
	Blob         string `json:"-"`
	LastModified string `json:"last_modified"`
}

// S3Usage aggregates a bucket's contents.
type S3Usage struct {
	Bucket    string `json:"name"`
	AccountID string `json:"-"`
	Objects   int64  `json:"objects"`    // current (latest, non-delete-marker) objects
	Versions  int64  `json:"versions"`   // all stored versions incl. delete markers
	SizeBytes int64  `json:"size_bytes"` // bytes stored across all versions
}

// S3ListQuery parameterizes a delimiter-aware listing of current objects.
type S3ListQuery struct {
	Bucket    string
	Prefix    string
	Delimiter string
	Q         string
	Offset    int
	Limit     int
}

// S3Repo is the data access layer of the S3 module.
type S3Repo interface {
	InsertBucket(ctx context.Context, b *S3Bucket) error
	GetBucket(ctx context.Context, name string) (*S3Bucket, error)
	ListBuckets(ctx context.Context, account string) ([]S3Bucket, error)
	AllBuckets(ctx context.Context) ([]S3Bucket, error)
	SetBucketField(ctx context.Context, name, field, value string) error
	// DeleteBucket removes the bucket row (cascading to objects/uploads) and returns every blob id it referenced.
	DeleteBucket(ctx context.Context, name string) ([]string, error)
	Usage(ctx context.Context, account, bucket string) ([]S3Usage, error)
	StorageClassUsage(ctx context.Context, account string) (map[string]int64, error)
	HasContents(ctx context.Context, bucket string) (bool, error)

	// PutVersion inserts obj as the latest version of its key. When replaceNull is set the existing
	// 'null' version (if any) is removed first. Returns blob ids that are no longer referenced.
	PutVersion(ctx context.Context, obj *S3Object, replaceNull bool) ([]string, error)
	GetLatest(ctx context.Context, bucket, key string) (*S3Object, error)
	GetVersion(ctx context.Context, bucket, key, versionID string) (*S3Object, error)
	// DeleteVersion permanently removes one version, promoting the next newest to latest.
	DeleteVersion(ctx context.Context, bucket, key, versionID string) (*S3Object, error)
	ListObjects(ctx context.Context, q S3ListQuery) ([]S3Object, []string, int, error)
	ListVersions(ctx context.Context, bucket, prefix string, offset, limit int) ([]S3Object, int, error)
	AllVersions(ctx context.Context, bucket string, limit int) ([]S3Object, error)
	SetStorageClass(ctx context.Context, bucket, key, versionID, class string) error

	InsertUpload(ctx context.Context, u *S3Upload) error
	GetUpload(ctx context.Context, bucket, uploadID string) (*S3Upload, error)
	ListUploads(ctx context.Context, bucket string) ([]S3Upload, error)
	// PutPart upserts a part and returns the replaced part's blob id ('' if none).
	PutPart(ctx context.Context, p *S3Part) (string, error)
	ListParts(ctx context.Context, uploadID string) ([]S3Part, error)
	// DeleteUpload removes the upload and its parts, returning the parts' blob ids.
	DeleteUpload(ctx context.Context, uploadID string) ([]string, error)
	// CompleteUpload atomically stores obj (like PutVersion) and removes the upload; returns unreferenced blobs.
	CompleteUpload(ctx context.Context, uploadID string, obj *S3Object, replaceNull bool) ([]string, error)
}

// NewS3Repo returns the SQLite implementation.
func NewS3Repo(d *db.DB) S3Repo { return &s3Repo{d} }

type s3Repo struct{ d *db.DB }

const bucketCols = `name, account_id, acl, versioning, policy, lifecycle, created_at`

func scanBucket(sc interface{ Scan(...any) error }) (*S3Bucket, error) {
	var b S3Bucket
	var lc string
	if err := sc.Scan(&b.Name, &b.AccountID, &b.ACL, &b.Versioning, &b.Policy, &lc, &b.CreatedAt); err != nil {
		return nil, err
	}
	b.Lifecycle = json.RawMessage(lc)
	return &b, nil
}

func (r *s3Repo) InsertBucket(ctx context.Context, b *S3Bucket) error {
	if b.CreatedAt == "" {
		b.CreatedAt = db.Now()
	}
	if len(b.Lifecycle) == 0 {
		b.Lifecycle = json.RawMessage("[]")
	}
	_, err := r.d.ExecContext(ctx, `INSERT INTO s3_buckets(`+bucketCols+`) VALUES(?,?,?,?,?,?,?)`,
		b.Name, b.AccountID, b.ACL, b.Versioning, b.Policy, string(b.Lifecycle), b.CreatedAt)
	return err
}

func (r *s3Repo) GetBucket(ctx context.Context, name string) (*S3Bucket, error) {
	b, err := scanBucket(r.d.QueryRowContext(ctx, `SELECT `+bucketCols+` FROM s3_buckets WHERE name=?`, name))
	return b, notFound(err)
}

func (r *s3Repo) listBuckets(ctx context.Context, where string, args ...any) ([]S3Bucket, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+bucketCols+` FROM s3_buckets `+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []S3Bucket{}
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (r *s3Repo) ListBuckets(ctx context.Context, account string) ([]S3Bucket, error) {
	return r.listBuckets(ctx, `WHERE account_id=?`, account)
}

func (r *s3Repo) AllBuckets(ctx context.Context) ([]S3Bucket, error) { return r.listBuckets(ctx, "") }

var bucketFields = map[string]bool{"acl": true, "versioning": true, "policy": true, "lifecycle": true}

func (r *s3Repo) SetBucketField(ctx context.Context, name, field, value string) error {
	if !bucketFields[field] {
		return sql.ErrNoRows
	}
	res, err := r.d.ExecContext(ctx, `UPDATE s3_buckets SET `+field+`=? WHERE name=?`, value, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func collectStrings(ctx context.Context, q Querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

func (r *s3Repo) DeleteBucket(ctx context.Context, name string) ([]string, error) {
	var blobs []string
	err := r.d.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		blobs, err = collectStrings(ctx, tx, `SELECT blob FROM s3_objects WHERE bucket=? AND blob<>''
			UNION ALL SELECT p.blob FROM s3_multipart_parts p JOIN s3_multipart_uploads u ON u.upload_id=p.upload_id WHERE u.bucket=?`, name, name)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM s3_buckets WHERE name=?`, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	return blobs, err
}

func (r *s3Repo) Usage(ctx context.Context, account, bucket string) ([]S3Usage, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT b.name, b.account_id,
			COALESCE(SUM(CASE WHEN o.is_latest=1 AND o.delete_marker=0 THEN 1 ELSE 0 END),0),
			COUNT(o.id), COALESCE(SUM(o.size),0)
		FROM s3_buckets b LEFT JOIN s3_objects o ON o.bucket=b.name
		WHERE (?='' OR b.account_id=?) AND (?='' OR b.name=?)
		GROUP BY b.name ORDER BY b.name`, account, account, bucket, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []S3Usage{}
	for rows.Next() {
		var u S3Usage
		if err := rows.Scan(&u.Bucket, &u.AccountID, &u.Objects, &u.Versions, &u.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *s3Repo) StorageClassUsage(ctx context.Context, account string) (map[string]int64, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT o.storage_class, SUM(o.size) FROM s3_objects o JOIN s3_buckets b ON b.name=o.bucket
		WHERE b.account_id=? AND o.delete_marker=0 GROUP BY o.storage_class`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var c string
		var n int64
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

func (r *s3Repo) HasContents(ctx context.Context, bucket string) (bool, error) {
	var n int
	err := r.d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM s3_objects WHERE bucket=?) OR EXISTS(SELECT 1 FROM s3_multipart_uploads WHERE bucket=?)`, bucket, bucket).Scan(&n)
	return n != 0, err
}

// ---- objects

const objCols = `id, bucket, key, version_id, is_latest, delete_marker, size, etag, content_type, storage_class, metadata, blob, last_modified, noncurrent_since`

func scanObject(sc interface{ Scan(...any) error }) (*S3Object, error) {
	var o S3Object
	var md string
	var latest, dm int
	if err := sc.Scan(&o.ID, &o.Bucket, &o.Key, &o.VersionID, &latest, &dm, &o.Size, &o.ETag, &o.ContentType, &o.StorageClass, &md, &o.Blob, &o.LastModified, &o.NoncurrentSince); err != nil {
		return nil, err
	}
	o.IsLatest, o.DeleteMarker = latest == 1, dm == 1
	o.Metadata = map[string]string{}
	_ = json.Unmarshal([]byte(md), &o.Metadata)
	return &o, nil
}

func queryObjects(ctx context.Context, q Querier, query string, args ...any) ([]S3Object, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []S3Object{}
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

func putVersion(ctx context.Context, tx *sql.Tx, obj *S3Object, replaceNull bool) ([]string, error) {
	var freed []string
	now := db.Now()
	if obj.LastModified == "" {
		obj.LastModified = now
	}
	if replaceNull {
		var blob string
		err := tx.QueryRowContext(ctx, `DELETE FROM s3_objects WHERE bucket=? AND key=? AND version_id='null' RETURNING blob`, obj.Bucket, obj.Key).Scan(&blob)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if blob != "" {
			freed = append(freed, blob)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE s3_objects SET is_latest=0, noncurrent_since=? WHERE bucket=? AND key=? AND is_latest=1`, obj.LastModified, obj.Bucket, obj.Key); err != nil {
		return nil, err
	}
	md, _ := json.Marshal(obj.Metadata)
	if obj.Metadata == nil {
		md = []byte("{}")
	}
	if obj.StorageClass == "" {
		obj.StorageClass = "STANDARD"
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO s3_objects(bucket, key, version_id, is_latest, delete_marker, size, etag, content_type, storage_class, metadata, blob, last_modified)
		VALUES(?,?,?,1,?,?,?,?,?,?,?,?)`, obj.Bucket, obj.Key, obj.VersionID, b2i(obj.DeleteMarker), obj.Size, obj.ETag, obj.ContentType, obj.StorageClass, string(md), obj.Blob, obj.LastModified)
	if err != nil {
		return nil, err
	}
	obj.ID, _ = res.LastInsertId()
	obj.IsLatest = true
	return freed, nil
}

func (r *s3Repo) PutVersion(ctx context.Context, obj *S3Object, replaceNull bool) ([]string, error) {
	var freed []string
	err := r.d.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		freed, err = putVersion(ctx, tx, obj, replaceNull)
		return err
	})
	return freed, err
}

func (r *s3Repo) GetLatest(ctx context.Context, bucket, key string) (*S3Object, error) {
	o, err := scanObject(r.d.QueryRowContext(ctx, `SELECT `+objCols+` FROM s3_objects WHERE bucket=? AND key=? AND is_latest=1`, bucket, key))
	return o, notFound(err)
}

func (r *s3Repo) GetVersion(ctx context.Context, bucket, key, versionID string) (*S3Object, error) {
	o, err := scanObject(r.d.QueryRowContext(ctx, `SELECT `+objCols+` FROM s3_objects WHERE bucket=? AND key=? AND version_id=?`, bucket, key, versionID))
	return o, notFound(err)
}

func (r *s3Repo) DeleteVersion(ctx context.Context, bucket, key, versionID string) (*S3Object, error) {
	var out *S3Object
	err := r.d.Tx(ctx, func(tx *sql.Tx) error {
		o, err := scanObject(tx.QueryRowContext(ctx, `SELECT `+objCols+` FROM s3_objects WHERE bucket=? AND key=? AND version_id=?`, bucket, key, versionID))
		if err != nil {
			return notFound(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM s3_objects WHERE id=?`, o.ID); err != nil {
			return err
		}
		if o.IsLatest {
			if _, err := tx.ExecContext(ctx, `UPDATE s3_objects SET is_latest=1, noncurrent_since=NULL WHERE id=(SELECT MAX(id) FROM s3_objects WHERE bucket=? AND key=?)`, bucket, key); err != nil {
				return err
			}
		}
		out = o
		return nil
	})
	return out, err
}

// prefixUpper returns the smallest string greater than every string starting with prefix
// (byte-wise, matching SQLite's BINARY collation), or "" when unbounded.
func prefixUpper(prefix string) string {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xFF {
			b[i]++
			return string(b[:i+1])
		}
	}
	return ""
}

func (r *s3Repo) ListObjects(ctx context.Context, q S3ListQuery) ([]S3Object, []string, int, error) {
	plen := utf8.RuneCountInString(q.Prefix)
	upper := prefixUpper(q.Prefix)
	// Entries are either a common prefix (keys sharing prefix+…+delimiter) or a current object;
	// both are paginated together in key order, like S3's MaxKeys.
	cte := `WITH k AS (
		SELECT id, key,
			CASE WHEN ?1 <> '' AND instr(substr(key, ?2 + 1), ?1) > 0
				THEN substr(key, 1, ?2 + instr(substr(key, ?2 + 1), ?1) + length(?1) - 1) END AS cp
		FROM s3_objects
		WHERE bucket = ?3 AND is_latest = 1 AND delete_marker = 0 AND key >= ?4 AND (?5 = '' OR key < ?5)
			AND (?6 = '' OR instr(lower(key), lower(?6)) > 0)
	), e AS (
		SELECT DISTINCT cp AS name, 1 AS is_prefix, 0 AS oid FROM k WHERE cp IS NOT NULL
		UNION ALL
		SELECT key, 0, id FROM k WHERE cp IS NULL
	) `
	args := []any{q.Delimiter, plen, q.Bucket, q.Prefix, upper, q.Q}
	var total int
	if err := r.d.QueryRowContext(ctx, cte+`SELECT COUNT(*) FROM e`, args...).Scan(&total); err != nil {
		return nil, nil, 0, err
	}
	rows, err := r.d.QueryContext(ctx, cte+`SELECT name, is_prefix, oid FROM e ORDER BY name LIMIT ?7 OFFSET ?8`, append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, nil, 0, err
	}
	prefixes := []string{}
	var ids []any
	for rows.Next() {
		var name string
		var isPrefix int
		var oid int64
		if err := rows.Scan(&name, &isPrefix, &oid); err != nil {
			rows.Close()
			return nil, nil, 0, err
		}
		if isPrefix == 1 {
			prefixes = append(prefixes, name)
		} else {
			ids = append(ids, oid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}
	objs := []S3Object{}
	if len(ids) > 0 {
		objs, err = queryObjects(ctx, r.d, `SELECT `+objCols+` FROM s3_objects WHERE id IN (?`+strings.Repeat(",?", len(ids)-1)+`) ORDER BY key`, ids...)
		if err != nil {
			return nil, nil, 0, err
		}
	}
	return objs, prefixes, total, nil
}

func (r *s3Repo) ListVersions(ctx context.Context, bucket, prefix string, offset, limit int) ([]S3Object, int, error) {
	upper := prefixUpper(prefix)
	const where = ` FROM s3_objects WHERE bucket=?1 AND key>=?2 AND (?3='' OR key<?3)`
	var total int
	if err := r.d.QueryRowContext(ctx, `SELECT COUNT(*)`+where, bucket, prefix, upper).Scan(&total); err != nil {
		return nil, 0, err
	}
	objs, err := queryObjects(ctx, r.d, `SELECT `+objCols+where+` ORDER BY key, id DESC LIMIT ?4 OFFSET ?5`, bucket, prefix, upper, limit, offset)
	return objs, total, err
}

func (r *s3Repo) AllVersions(ctx context.Context, bucket string, limit int) ([]S3Object, error) {
	return queryObjects(ctx, r.d, `SELECT `+objCols+` FROM s3_objects WHERE bucket=? ORDER BY key, id DESC LIMIT ?`, bucket, limit)
}

func (r *s3Repo) SetStorageClass(ctx context.Context, bucket, key, versionID, class string) error {
	res, err := r.d.ExecContext(ctx, `UPDATE s3_objects SET storage_class=? WHERE bucket=? AND key=? AND version_id=? AND delete_marker=0`, class, bucket, key, versionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- multipart

func (r *s3Repo) InsertUpload(ctx context.Context, u *S3Upload) error {
	md, _ := json.Marshal(u.Metadata)
	if u.Metadata == nil {
		md = []byte("{}")
	}
	if u.Initiated == "" {
		u.Initiated = db.Now()
	}
	_, err := r.d.ExecContext(ctx, `INSERT INTO s3_multipart_uploads(upload_id, bucket, key, content_type, metadata, initiated) VALUES(?,?,?,?,?,?)`,
		u.UploadID, u.Bucket, u.Key, u.ContentType, string(md), u.Initiated)
	return err
}

const uploadSelect = `SELECT u.upload_id, u.bucket, u.key, u.content_type, u.metadata, u.initiated,
	(SELECT COUNT(*) FROM s3_multipart_parts p WHERE p.upload_id=u.upload_id),
	(SELECT COALESCE(SUM(size),0) FROM s3_multipart_parts p WHERE p.upload_id=u.upload_id)
	FROM s3_multipart_uploads u `

func scanUpload(sc interface{ Scan(...any) error }) (*S3Upload, error) {
	var u S3Upload
	var md string
	if err := sc.Scan(&u.UploadID, &u.Bucket, &u.Key, &u.ContentType, &md, &u.Initiated, &u.Parts, &u.SizeBytes); err != nil {
		return nil, err
	}
	u.Metadata = map[string]string{}
	_ = json.Unmarshal([]byte(md), &u.Metadata)
	return &u, nil
}

func (r *s3Repo) GetUpload(ctx context.Context, bucket, uploadID string) (*S3Upload, error) {
	u, err := scanUpload(r.d.QueryRowContext(ctx, uploadSelect+`WHERE u.bucket=? AND u.upload_id=?`, bucket, uploadID))
	return u, notFound(err)
}

func (r *s3Repo) ListUploads(ctx context.Context, bucket string) ([]S3Upload, error) {
	rows, err := r.d.QueryContext(ctx, uploadSelect+`WHERE u.bucket=? ORDER BY u.key, u.initiated`, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []S3Upload{}
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (r *s3Repo) PutPart(ctx context.Context, p *S3Part) (string, error) {
	var old string
	if p.LastModified == "" {
		p.LastModified = db.Now()
	}
	err := r.d.Tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT blob FROM s3_multipart_parts WHERE upload_id=? AND part_number=?`, p.UploadID, p.PartNumber).Scan(&old)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO s3_multipart_parts(upload_id, part_number, size, etag, blob, last_modified) VALUES(?,?,?,?,?,?)
			ON CONFLICT(upload_id, part_number) DO UPDATE SET size=excluded.size, etag=excluded.etag, blob=excluded.blob, last_modified=excluded.last_modified`,
			p.UploadID, p.PartNumber, p.Size, p.ETag, p.Blob, p.LastModified)
		return err
	})
	return old, err
}

func (r *s3Repo) ListParts(ctx context.Context, uploadID string) ([]S3Part, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT upload_id, part_number, size, etag, blob, last_modified FROM s3_multipart_parts WHERE upload_id=? ORDER BY part_number`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []S3Part{}
	for rows.Next() {
		var p S3Part
		if err := rows.Scan(&p.UploadID, &p.PartNumber, &p.Size, &p.ETag, &p.Blob, &p.LastModified); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func deleteUpload(ctx context.Context, tx *sql.Tx, uploadID string) ([]string, error) {
	blobs, err := collectStrings(ctx, tx, `SELECT blob FROM s3_multipart_parts WHERE upload_id=?`, uploadID)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM s3_multipart_uploads WHERE upload_id=?`, uploadID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return blobs, nil
}

func (r *s3Repo) DeleteUpload(ctx context.Context, uploadID string) ([]string, error) {
	var blobs []string
	err := r.d.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		blobs, err = deleteUpload(ctx, tx, uploadID)
		return err
	})
	return blobs, err
}

func (r *s3Repo) CompleteUpload(ctx context.Context, uploadID string, obj *S3Object, replaceNull bool) ([]string, error) {
	var freed []string
	err := r.d.Tx(ctx, func(tx *sql.Tx) error {
		parts, err := deleteUpload(ctx, tx, uploadID)
		if err != nil {
			return err
		}
		f, err := putVersion(ctx, tx, obj, replaceNull)
		if err != nil {
			return err
		}
		freed = append(parts, f...)
		return nil
	})
	return freed, err
}

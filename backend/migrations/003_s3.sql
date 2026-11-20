-- 003_s3: S3-style object storage (buckets, object versions, multipart uploads).
-- Object bytes live on disk under OBJECT_STORAGE_DIR/<bucket>/<blob id>; only metadata is stored here.

CREATE TABLE s3_buckets (
    name        TEXT PRIMARY KEY,                     -- globally unique across the platform
    account_id  TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    acl         TEXT NOT NULL DEFAULT 'private' CHECK (acl IN ('private', 'public-read')),
    versioning  TEXT NOT NULL DEFAULT 'Disabled' CHECK (versioning IN ('Disabled', 'Enabled', 'Suspended')),
    policy      TEXT NOT NULL DEFAULT '',             -- bucket policy JSON ('' = none)
    lifecycle   TEXT NOT NULL DEFAULT '[]',           -- lifecycle rules JSON array
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_s3_buckets_account ON s3_buckets(account_id, name);

CREATE TABLE s3_objects (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    bucket           TEXT NOT NULL REFERENCES s3_buckets(name) ON DELETE CASCADE,
    key              TEXT NOT NULL,
    version_id       TEXT NOT NULL,                   -- 'null' for the null version (unversioned / suspended)
    is_latest        INTEGER NOT NULL DEFAULT 1 CHECK (is_latest IN (0, 1)),
    delete_marker    INTEGER NOT NULL DEFAULT 0 CHECK (delete_marker IN (0, 1)),
    size             INTEGER NOT NULL DEFAULT 0 CHECK (size >= 0),
    etag             TEXT NOT NULL DEFAULT '',
    content_type     TEXT NOT NULL DEFAULT '',
    storage_class    TEXT NOT NULL DEFAULT 'STANDARD',
    metadata         TEXT NOT NULL DEFAULT '{}',
    blob             TEXT NOT NULL DEFAULT '',        -- random blob id (file name inside the bucket dir); '' for delete markers
    last_modified    TEXT NOT NULL,
    noncurrent_since TEXT,
    UNIQUE (bucket, key, version_id)
);
-- latest-version lookups and prefix range scans of the current listing
CREATE INDEX idx_s3_objects_latest ON s3_objects(bucket, is_latest, key);
-- per-key version history and (bucket,key) prefix queries over all versions
CREATE INDEX idx_s3_objects_key ON s3_objects(bucket, key, is_latest);
-- at most one latest version per key
CREATE UNIQUE INDEX idx_s3_objects_one_latest ON s3_objects(bucket, key) WHERE is_latest = 1;

CREATE TABLE s3_multipart_uploads (
    upload_id     TEXT PRIMARY KEY,
    bucket        TEXT NOT NULL REFERENCES s3_buckets(name) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    metadata      TEXT NOT NULL DEFAULT '{}',
    initiated     TEXT NOT NULL
);
CREATE INDEX idx_s3_mpu_bucket ON s3_multipart_uploads(bucket, key);

CREATE TABLE s3_multipart_parts (
    upload_id    TEXT NOT NULL REFERENCES s3_multipart_uploads(upload_id) ON DELETE CASCADE,
    part_number  INTEGER NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
    size         INTEGER NOT NULL,
    etag         TEXT NOT NULL,
    blob         TEXT NOT NULL,
    last_modified TEXT NOT NULL,
    PRIMARY KEY (upload_id, part_number)
);

-- 008_messaging_data: SQS queues/messages, SNS topics/subscriptions/deliveries,
-- Secrets Manager secrets/versions, DynamoDB tables/items, Route 53 zones/records.

-- ---------------------------------------------------------------- SQS
CREATE TABLE sqs_queues (
    queue_id           TEXT PRIMARY KEY,
    account_id         TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    visibility_timeout INTEGER NOT NULL DEFAULT 30 CHECK (visibility_timeout BETWEEN 0 AND 43200),
    message_retention  INTEGER NOT NULL DEFAULT 345600 CHECK (message_retention BETWEEN 60 AND 1209600),
    delay_seconds      INTEGER NOT NULL DEFAULT 0 CHECK (delay_seconds BETWEEN 0 AND 900),
    max_receive_count  INTEGER NOT NULL DEFAULT 0 CHECK (max_receive_count BETWEEN 0 AND 1000),
    dlq_queue_id       TEXT REFERENCES sqs_queues(queue_id) ON DELETE RESTRICT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    UNIQUE (account_id, name)
);

CREATE TABLE sqs_messages (
    message_id        TEXT PRIMARY KEY,
    queue_id          TEXT NOT NULL REFERENCES sqs_queues(queue_id) ON DELETE CASCADE,
    body              TEXT NOT NULL,
    md5_of_body       TEXT NOT NULL,
    sent_at           TEXT NOT NULL,
    visible_at        TEXT NOT NULL,
    expires_at        TEXT NOT NULL,
    receive_count     INTEGER NOT NULL DEFAULT 0 CHECK (receive_count >= 0),
    receipt_handle    TEXT,
    first_received_at TEXT
);
CREATE INDEX idx_sqs_messages_visible ON sqs_messages(queue_id, visible_at);
CREATE INDEX idx_sqs_messages_expires ON sqs_messages(expires_at);
CREATE UNIQUE INDEX idx_sqs_messages_receipt ON sqs_messages(receipt_handle) WHERE receipt_handle IS NOT NULL;

-- ---------------------------------------------------------------- SNS
CREATE TABLE sns_topics (
    topic_id     TEXT PRIMARY KEY,
    account_id   TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    UNIQUE (account_id, name)
);

CREATE TABLE sns_subscriptions (
    subscription_id TEXT PRIMARY KEY,
    topic_id        TEXT NOT NULL REFERENCES sns_topics(topic_id) ON DELETE CASCADE,
    account_id      TEXT NOT NULL,
    protocol        TEXT NOT NULL CHECK (protocol IN ('sqs','log')),
    endpoint        TEXT NOT NULL,
    raw_delivery    INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    UNIQUE (topic_id, protocol, endpoint)
);
CREATE INDEX idx_sns_subs_topic ON sns_subscriptions(topic_id);

CREATE TABLE sns_deliveries (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    topic_id        TEXT NOT NULL REFERENCES sns_topics(topic_id) ON DELETE CASCADE,
    message_id      TEXT NOT NULL,
    subscription_id TEXT NOT NULL,
    protocol        TEXT NOT NULL,
    endpoint        TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('delivered','failed')),
    error           TEXT NOT NULL DEFAULT '',
    ts              TEXT NOT NULL
);
CREATE INDEX idx_sns_deliveries_topic ON sns_deliveries(topic_id, id);

-- ---------------------------------------------------------------- Secrets Manager
CREATE TABLE secrets (
    secret_id          TEXT PRIMARY KEY,
    account_id         TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    current_version_id TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    last_accessed_at   TEXT,
    UNIQUE (account_id, name)
);

-- Values are AES-256-GCM ciphertext (nonce || sealed), never plaintext.
CREATE TABLE secret_versions (
    version_id TEXT PRIMARY KEY,
    secret_id  TEXT NOT NULL REFERENCES secrets(secret_id) ON DELETE CASCADE,
    ciphertext BLOB NOT NULL,
    stage      TEXT NOT NULL DEFAULT '' CHECK (stage IN ('AWSCURRENT','AWSPREVIOUS','')),
    created_at TEXT NOT NULL
);
CREATE INDEX idx_secret_versions_secret ON secret_versions(secret_id, created_at);

-- ---------------------------------------------------------------- DynamoDB
CREATE TABLE ddb_tables (
    table_id   TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    pk_name    TEXT NOT NULL,
    pk_type    TEXT NOT NULL CHECK (pk_type IN ('S','N')),
    sk_name    TEXT NOT NULL DEFAULT '',
    sk_type    TEXT NOT NULL DEFAULT '' CHECK (sk_type IN ('S','N','')),
    created_at TEXT NOT NULL,
    UNIQUE (account_id, name)
);

-- pk/sk hold the canonical string form of the key; sk_num the numeric value for N sort keys.
CREATE TABLE ddb_items (
    table_id   TEXT NOT NULL REFERENCES ddb_tables(table_id) ON DELETE CASCADE,
    pk         TEXT NOT NULL,
    sk         TEXT NOT NULL DEFAULT '',
    sk_num     REAL,
    item       TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (table_id, pk, sk)
);
CREATE INDEX idx_ddb_items_num ON ddb_items(table_id, pk, sk_num);

-- ---------------------------------------------------------------- Route 53
CREATE TABLE r53_zones (
    zone_id    TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    private    INTEGER NOT NULL DEFAULT 0,
    vpc_id     TEXT NOT NULL DEFAULT '',
    comment    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    CHECK ((private = 1 AND vpc_id <> '') OR (private = 0 AND vpc_id = ''))
);
CREATE INDEX idx_r53_zones_account ON r53_zones(account_id, name);

CREATE TABLE r53_records (
    zone_id     TEXT NOT NULL REFERENCES r53_zones(zone_id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('A','AAAA','CNAME','TXT','NS','SOA')),
    ttl         INTEGER NOT NULL CHECK (ttl BETWEEN 0 AND 604800),
    value_json  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (zone_id, name, type)
);

-- 001_core: platform users, sessions, projects, events, audit, jobs, IAM.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email         TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin','user')),
    account_id    TEXT NOT NULL UNIQUE,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE sessions (
    id_hash     TEXT PRIMARY KEY,            -- sha256 of the session token; the raw token is only in the cookie
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    last_seen   TEXT NOT NULL,
    ip          TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- A project is a logical workspace within an account (default project created per user).
CREATE TABLE projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    UNIQUE(account_id, name)
);

CREATE TABLE events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id    TEXT NOT NULL,
    ts            TEXT NOT NULL,
    resource_id   TEXT NOT NULL DEFAULT '',
    resource_type TEXT NOT NULL DEFAULT '',
    type          TEXT NOT NULL,
    message       TEXT NOT NULL,
    detail        TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_events_account_ts ON events(account_id, id DESC);
CREATE INDEX idx_events_resource ON events(resource_id, id DESC);

CREATE TABLE audit_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  TEXT NOT NULL DEFAULT '',
    ts          TEXT NOT NULL,
    principal   TEXT NOT NULL,
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL DEFAULT '',
    result      TEXT NOT NULL CHECK (result IN ('allowed','denied','error')),
    status_code INTEGER NOT NULL,
    request_id  TEXT NOT NULL,
    source_ip   TEXT NOT NULL DEFAULT '',
    detail      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_audit_account_ts ON audit_logs(account_id, id DESC);
CREATE INDEX idx_audit_action ON audit_logs(action);

CREATE TABLE background_jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    type         TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT NOT NULL DEFAULT '',
    run_after    TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_jobs_status_run ON background_jobs(status, run_after);

CREATE TABLE kv_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- IAM ------------------------------------------------------------------------

CREATE TABLE iam_users (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    user_name   TEXT NOT NULL,
    user_id     TEXT NOT NULL UNIQUE,
    path        TEXT NOT NULL DEFAULT '/',
    tags        TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL,
    UNIQUE(account_id, user_name)
);

CREATE TABLE iam_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    group_name  TEXT NOT NULL,
    group_id    TEXT NOT NULL UNIQUE,
    path        TEXT NOT NULL DEFAULT '/',
    created_at  TEXT NOT NULL,
    UNIQUE(account_id, group_name)
);

CREATE TABLE iam_group_members (
    group_id INTEGER NOT NULL REFERENCES iam_groups(id) ON DELETE CASCADE,
    user_id  INTEGER NOT NULL REFERENCES iam_users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX idx_group_members_user ON iam_group_members(user_id);

CREATE TABLE iam_roles (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id           TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    role_name            TEXT NOT NULL,
    role_id              TEXT NOT NULL UNIQUE,
    path                 TEXT NOT NULL DEFAULT '/',
    description          TEXT NOT NULL DEFAULT '',
    assume_role_policy   TEXT NOT NULL,
    max_session_duration INTEGER NOT NULL DEFAULT 3600,
    created_at           TEXT NOT NULL,
    UNIQUE(account_id, role_name)
);

-- account_id '' = built-in (platform-provided) managed policy, readable by everyone.
CREATE TABLE iam_policies (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  TEXT NOT NULL,
    policy_name TEXT NOT NULL,
    policy_id   TEXT NOT NULL UNIQUE,
    arn         TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    document    TEXT NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1,
    builtin     INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE(account_id, policy_name)
);

-- principal_type: user | group | role
CREATE TABLE iam_policy_attachments (
    policy_id      INTEGER NOT NULL REFERENCES iam_policies(id) ON DELETE CASCADE,
    account_id     TEXT NOT NULL,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('user','group','role')),
    principal_id   INTEGER NOT NULL,
    PRIMARY KEY (policy_id, principal_type, principal_id)
);
CREATE INDEX idx_attach_principal ON iam_policy_attachments(principal_type, principal_id);

CREATE TABLE iam_inline_policies (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id     TEXT NOT NULL,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('user','group','role')),
    principal_id   INTEGER NOT NULL,
    policy_name    TEXT NOT NULL,
    document       TEXT NOT NULL,
    UNIQUE(principal_type, principal_id, policy_name)
);

-- Long-term keys (AKIA…) for IAM users and temporary keys (ASIA…) issued by sts:AssumeRole.
CREATE TABLE iam_access_keys (
    access_key_id  TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    iam_user_id    INTEGER REFERENCES iam_users(id) ON DELETE CASCADE,
    role_id        INTEGER REFERENCES iam_roles(id) ON DELETE CASCADE,
    session_name   TEXT NOT NULL DEFAULT '',
    secret_hash    TEXT NOT NULL,          -- sha256(secret) — secrets are high-entropy random, so a fast hash is appropriate
    session_token_hash TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL CHECK (status IN ('Active','Inactive')),
    expires_at     TEXT,
    last_used_at   TEXT,
    created_at     TEXT NOT NULL,
    CHECK ((iam_user_id IS NULL) <> (role_id IS NULL))
);
CREATE INDEX idx_access_keys_user ON iam_access_keys(iam_user_id);

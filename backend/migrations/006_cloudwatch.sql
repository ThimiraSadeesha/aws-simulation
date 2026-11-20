-- 006_cloudwatch: metrics, alarms (+history) and logs.

CREATE TABLE cw_metric_data (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  TEXT NOT NULL,
    namespace   TEXT NOT NULL,
    metric_name TEXT NOT NULL,
    dim_key     TEXT NOT NULL DEFAULT '{}', -- canonical JSON of the dimensions (sorted keys)
    ts          TEXT NOT NULL,
    value       REAL NOT NULL,
    unit        TEXT NOT NULL DEFAULT 'None'
);
CREATE INDEX idx_cw_metric_series ON cw_metric_data(account_id, namespace, metric_name, dim_key, ts);
CREATE INDEX idx_cw_metric_ts ON cw_metric_data(ts);

CREATE TABLE cw_alarms (
    alarm_id            TEXT PRIMARY KEY,
    account_id          TEXT NOT NULL,
    alarm_name          TEXT NOT NULL,
    namespace           TEXT NOT NULL,
    metric_name         TEXT NOT NULL,
    dim_key             TEXT NOT NULL DEFAULT '{}',
    statistic           TEXT NOT NULL CHECK (statistic IN ('Average','Maximum','Minimum','Sum','SampleCount')),
    period              INTEGER NOT NULL CHECK (period > 0),
    evaluation_periods  INTEGER NOT NULL CHECK (evaluation_periods > 0),
    threshold           REAL NOT NULL,
    comparison_operator TEXT NOT NULL,
    alarm_actions       TEXT NOT NULL DEFAULT '[]',
    state               TEXT NOT NULL DEFAULT 'INSUFFICIENT_DATA' CHECK (state IN ('OK','ALARM','INSUFFICIENT_DATA')),
    state_reason        TEXT NOT NULL DEFAULT '',
    state_updated_at    TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    UNIQUE(account_id, alarm_name)
);

CREATE TABLE cw_alarm_history (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    alarm_id  TEXT NOT NULL REFERENCES cw_alarms(alarm_id) ON DELETE CASCADE,
    ts        TEXT NOT NULL,
    type      TEXT NOT NULL DEFAULT 'StateUpdate',
    old_state TEXT NOT NULL DEFAULT '',
    new_state TEXT NOT NULL DEFAULT '',
    summary   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_cw_alarm_history ON cw_alarm_history(alarm_id, id DESC);

CREATE TABLE cw_log_groups (
    log_group_id   INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id     TEXT NOT NULL,
    name           TEXT NOT NULL,
    retention_days INTEGER,
    created_at     TEXT NOT NULL,
    UNIQUE(account_id, name)
);

CREATE TABLE cw_log_streams (
    log_stream_id INTEGER PRIMARY KEY AUTOINCREMENT,
    log_group_id  INTEGER NOT NULL REFERENCES cw_log_groups(log_group_id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_event_ts TEXT,
    UNIQUE(log_group_id, name)
);

CREATE TABLE cw_log_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    log_group_id  INTEGER NOT NULL REFERENCES cw_log_groups(log_group_id) ON DELETE CASCADE,
    log_stream_id INTEGER NOT NULL REFERENCES cw_log_streams(log_stream_id) ON DELETE CASCADE,
    ts            TEXT NOT NULL,
    message       TEXT NOT NULL,
    ingested_at   TEXT NOT NULL
);
CREATE INDEX idx_cw_log_events_group_ts ON cw_log_events(log_group_id, ts);
CREATE INDEX idx_cw_log_events_stream ON cw_log_events(log_stream_id);

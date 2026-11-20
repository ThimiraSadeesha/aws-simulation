-- 007_labs: training-lab progress per account and per-step validation results.
-- Lab definitions themselves live in files (LABS_DIR); only progress is stored here.

CREATE TABLE lab_progress (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id   TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    lab_id       TEXT NOT NULL CHECK (length(lab_id) BETWEEN 3 AND 64),
    status       TEXT NOT NULL CHECK (status IN ('in_progress','completed')),
    total_steps  INTEGER NOT NULL CHECK (total_steps > 0),
    started_at   TEXT NOT NULL,
    completed_at TEXT,
    updated_at   TEXT NOT NULL,
    UNIQUE (account_id, lab_id)
);
CREATE INDEX idx_lab_progress_account ON lab_progress(account_id, status);

CREATE TABLE lab_step_results (
    progress_id INTEGER NOT NULL REFERENCES lab_progress(id) ON DELETE CASCADE,
    step_index  INTEGER NOT NULL CHECK (step_index >= 0),
    status      TEXT NOT NULL CHECK (status IN ('passed','failed')),
    message     TEXT NOT NULL DEFAULT '',
    details     TEXT NOT NULL DEFAULT '{}',
    attempts    INTEGER NOT NULL DEFAULT 1 CHECK (attempts > 0),
    checked_at  TEXT NOT NULL,
    PRIMARY KEY (progress_id, step_index)
);

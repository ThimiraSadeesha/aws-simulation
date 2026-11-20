-- 005_autoscaling: launch templates, Auto Scaling groups, scaling policies, activity history.

CREATE TABLE asg_launch_templates (
    launch_template_id TEXT PRIMARY KEY,
    account_id         TEXT NOT NULL,
    name               TEXT NOT NULL,
    image_id           TEXT NOT NULL,
    instance_type      TEXT NOT NULL,
    security_group_ids TEXT NOT NULL DEFAULT '[]', -- JSON array
    key_name           TEXT NOT NULL DEFAULT '',
    user_data          TEXT NOT NULL DEFAULT '',
    workload_profile   TEXT NOT NULL DEFAULT 'steady',
    created_at         TEXT NOT NULL,
    UNIQUE(account_id, name)
);

CREATE TABLE asg_groups (
    group_id            TEXT PRIMARY KEY,
    account_id          TEXT NOT NULL,
    name                TEXT NOT NULL,
    launch_template_id  TEXT NOT NULL REFERENCES asg_launch_templates(launch_template_id) ON DELETE RESTRICT,
    vpc_id              TEXT NOT NULL,
    min_size            INTEGER NOT NULL CHECK (min_size >= 0),
    max_size            INTEGER NOT NULL CHECK (max_size >= min_size),
    desired_capacity    INTEGER NOT NULL CHECK (desired_capacity BETWEEN min_size AND max_size),
    cooldown_s          INTEGER NOT NULL DEFAULT 60 CHECK (cooldown_s >= 0),
    health_check_type   TEXT NOT NULL DEFAULT 'EC2' CHECK (health_check_type IN ('EC2','ELB')),
    health_check_grace_s INTEGER NOT NULL DEFAULT 60,
    last_scaling_at     TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    UNIQUE(account_id, name)
);
CREATE INDEX idx_asg_account ON asg_groups(account_id);

CREATE TABLE asg_group_subnets (
    group_id  TEXT NOT NULL REFERENCES asg_groups(group_id) ON DELETE CASCADE,
    subnet_id TEXT NOT NULL REFERENCES subnets(subnet_id) ON DELETE RESTRICT,
    position  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, subnet_id)
);

CREATE TABLE asg_group_target_groups (
    group_id        TEXT NOT NULL REFERENCES asg_groups(group_id) ON DELETE CASCADE,
    target_group_id TEXT NOT NULL REFERENCES elb_target_groups(target_group_id) ON DELETE RESTRICT,
    PRIMARY KEY (group_id, target_group_id)
);
CREATE INDEX idx_asg_tg_tg ON asg_group_target_groups(target_group_id);

CREATE TABLE asg_policies (
    policy_id    TEXT PRIMARY KEY,
    group_id     TEXT NOT NULL REFERENCES asg_groups(group_id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    type         TEXT NOT NULL CHECK (type IN ('target_tracking','step')),
    target_value REAL,
    threshold    REAL,
    comparison   TEXT,
    adjustment   INTEGER,
    created_at   TEXT NOT NULL,
    UNIQUE(group_id, name)
);

CREATE TABLE asg_activities (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id    TEXT NOT NULL REFERENCES asg_groups(group_id) ON DELETE CASCADE,
    account_id  TEXT NOT NULL,
    ts          TEXT NOT NULL,
    description TEXT NOT NULL,
    cause       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL CHECK (status IN ('Successful','Failed','InProgress'))
);
CREATE INDEX idx_asg_activities_group ON asg_activities(group_id, id DESC);

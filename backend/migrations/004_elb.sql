-- 004_elb: Elastic Load Balancing (target groups, targets, load balancers, listeners).

CREATE TABLE elb_target_groups (
    target_group_id        TEXT PRIMARY KEY,
    account_id             TEXT NOT NULL,
    name                   TEXT NOT NULL,
    protocol               TEXT NOT NULL CHECK (protocol IN ('HTTP','HTTPS','TCP')),
    port                   INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    vpc_id                 TEXT NOT NULL REFERENCES vpcs(vpc_id) ON DELETE RESTRICT,
    algorithm              TEXT NOT NULL DEFAULT 'round_robin'
                           CHECK (algorithm IN ('round_robin','least_outstanding_requests','weighted_random')),
    hc_path                TEXT NOT NULL DEFAULT '/',
    hc_interval_s          INTEGER NOT NULL DEFAULT 30,
    hc_healthy_threshold   INTEGER NOT NULL DEFAULT 2,
    hc_unhealthy_threshold INTEGER NOT NULL DEFAULT 2,
    created_at             TEXT NOT NULL,
    UNIQUE(account_id, name)
);
CREATE INDEX idx_elb_tg_account ON elb_target_groups(account_id);

CREATE TABLE elb_targets (
    target_group_id TEXT NOT NULL REFERENCES elb_target_groups(target_group_id) ON DELETE CASCADE,
    instance_id     TEXT NOT NULL REFERENCES instances(instance_id) ON DELETE CASCADE,
    port            INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    weight          INTEGER NOT NULL DEFAULT 1 CHECK (weight BETWEEN 0 AND 999),
    registered_at   TEXT NOT NULL,
    PRIMARY KEY (target_group_id, instance_id)
);
CREATE INDEX idx_elb_targets_instance ON elb_targets(instance_id);

CREATE TABLE elb_load_balancers (
    load_balancer_id TEXT PRIMARY KEY,
    account_id       TEXT NOT NULL,
    name             TEXT NOT NULL,
    dns_name         TEXT NOT NULL UNIQUE,
    scheme           TEXT NOT NULL CHECK (scheme IN ('internet-facing','internal')),
    state            TEXT NOT NULL,
    vpc_id           TEXT NOT NULL REFERENCES vpcs(vpc_id) ON DELETE RESTRICT,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    UNIQUE(account_id, name)
);
CREATE INDEX idx_elb_lb_account ON elb_load_balancers(account_id);

CREATE TABLE elb_lb_subnets (
    load_balancer_id TEXT NOT NULL REFERENCES elb_load_balancers(load_balancer_id) ON DELETE CASCADE,
    subnet_id        TEXT NOT NULL REFERENCES subnets(subnet_id) ON DELETE RESTRICT,
    PRIMARY KEY (load_balancer_id, subnet_id)
);
CREATE INDEX idx_elb_lb_subnets_subnet ON elb_lb_subnets(subnet_id);

CREATE TABLE elb_lb_security_groups (
    load_balancer_id TEXT NOT NULL REFERENCES elb_load_balancers(load_balancer_id) ON DELETE CASCADE,
    group_id         TEXT NOT NULL REFERENCES security_groups(group_id) ON DELETE RESTRICT,
    PRIMARY KEY (load_balancer_id, group_id)
);
CREATE INDEX idx_elb_lb_sg_group ON elb_lb_security_groups(group_id);

CREATE TABLE elb_listeners (
    listener_id             TEXT PRIMARY KEY,
    load_balancer_id        TEXT NOT NULL REFERENCES elb_load_balancers(load_balancer_id) ON DELETE CASCADE,
    protocol                TEXT NOT NULL CHECK (protocol IN ('HTTP','HTTPS','TCP')),
    port                    INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    default_target_group_id TEXT NOT NULL REFERENCES elb_target_groups(target_group_id) ON DELETE RESTRICT,
    created_at              TEXT NOT NULL,
    UNIQUE(load_balancer_id, port)
);
CREATE INDEX idx_elb_listeners_tg ON elb_listeners(default_target_group_id);

-- 002_compute_network: VPC networking and EC2.

CREATE TABLE resource_tags (
    resource_id TEXT NOT NULL,
    account_id  TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       TEXT NOT NULL,
    PRIMARY KEY (resource_id, key)
);
CREATE INDEX idx_tags_account_kv ON resource_tags(account_id, key, value);

CREATE TABLE vpcs (
    vpc_id               TEXT PRIMARY KEY,
    account_id           TEXT NOT NULL REFERENCES users(account_id) ON DELETE CASCADE,
    name                 TEXT NOT NULL DEFAULT '',
    cidr_block           TEXT NOT NULL,
    is_default           INTEGER NOT NULL DEFAULT 0,
    enable_dns_support   INTEGER NOT NULL DEFAULT 1,
    enable_dns_hostnames INTEGER NOT NULL DEFAULT 1,
    state                TEXT NOT NULL DEFAULT 'available',
    created_at           TEXT NOT NULL
);
CREATE INDEX idx_vpcs_account ON vpcs(account_id);

CREATE TABLE route_tables (
    route_table_id TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL,
    vpc_id         TEXT NOT NULL REFERENCES vpcs(vpc_id) ON DELETE CASCADE,
    name           TEXT NOT NULL DEFAULT '',
    is_main        INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL
);
CREATE INDEX idx_rtb_vpc ON route_tables(vpc_id);

CREATE TABLE routes (
    route_table_id   TEXT NOT NULL REFERENCES route_tables(route_table_id) ON DELETE CASCADE,
    destination_cidr TEXT NOT NULL,
    target_id        TEXT NOT NULL,
    PRIMARY KEY (route_table_id, destination_cidr)
);

CREATE TABLE network_acls (
    network_acl_id TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL,
    vpc_id         TEXT NOT NULL REFERENCES vpcs(vpc_id) ON DELETE CASCADE,
    name           TEXT NOT NULL DEFAULT '',
    is_default     INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL
);
CREATE INDEX idx_acl_vpc ON network_acls(vpc_id);

CREATE TABLE network_acl_entries (
    network_acl_id TEXT NOT NULL REFERENCES network_acls(network_acl_id) ON DELETE CASCADE,
    rule_number    INTEGER NOT NULL CHECK (rule_number BETWEEN 1 AND 32766),
    egress         INTEGER NOT NULL,
    protocol       TEXT NOT NULL,
    from_port      INTEGER NOT NULL,
    to_port        INTEGER NOT NULL,
    cidr           TEXT NOT NULL,
    action         TEXT NOT NULL CHECK (action IN ('allow','deny')),
    PRIMARY KEY (network_acl_id, egress, rule_number)
);

CREATE TABLE subnets (
    subnet_id               TEXT PRIMARY KEY,
    account_id              TEXT NOT NULL,
    vpc_id                  TEXT NOT NULL REFERENCES vpcs(vpc_id) ON DELETE RESTRICT,
    name                    TEXT NOT NULL DEFAULT '',
    cidr_block              TEXT NOT NULL,
    availability_zone       TEXT NOT NULL DEFAULT 'local-1a',
    map_public_ip_on_launch INTEGER NOT NULL DEFAULT 0,
    route_table_id          TEXT REFERENCES route_tables(route_table_id) ON DELETE SET NULL, -- explicit association; NULL = main
    network_acl_id          TEXT NOT NULL REFERENCES network_acls(network_acl_id),
    created_at              TEXT NOT NULL
);
CREATE INDEX idx_subnets_vpc ON subnets(vpc_id);
CREATE INDEX idx_subnets_account ON subnets(account_id);

CREATE TABLE internet_gateways (
    internet_gateway_id TEXT PRIMARY KEY,
    account_id          TEXT NOT NULL,
    name                TEXT NOT NULL DEFAULT '',
    vpc_id              TEXT REFERENCES vpcs(vpc_id) ON DELETE RESTRICT,
    created_at          TEXT NOT NULL
);
CREATE INDEX idx_igw_account ON internet_gateways(account_id);

CREATE TABLE nat_gateways (
    nat_gateway_id TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL,
    name           TEXT NOT NULL DEFAULT '',
    subnet_id      TEXT NOT NULL REFERENCES subnets(subnet_id) ON DELETE RESTRICT,
    vpc_id         TEXT NOT NULL,
    state          TEXT NOT NULL,
    public_ip      TEXT NOT NULL,
    private_ip     TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX idx_nat_account ON nat_gateways(account_id);

CREATE TABLE security_groups (
    group_id    TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL,
    vpc_id      TEXT NOT NULL REFERENCES vpcs(vpc_id) ON DELETE CASCADE,
    group_name  TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_default  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    UNIQUE(vpc_id, group_name)
);
CREATE INDEX idx_sg_account ON security_groups(account_id);

CREATE TABLE security_group_rules (
    rule_id                  TEXT PRIMARY KEY,
    group_id                 TEXT NOT NULL REFERENCES security_groups(group_id) ON DELETE CASCADE,
    direction                TEXT NOT NULL CHECK (direction IN ('ingress','egress')),
    protocol                 TEXT NOT NULL,
    from_port                INTEGER NOT NULL,
    to_port                  INTEGER NOT NULL,
    cidr                     TEXT NOT NULL DEFAULT '',
    source_security_group_id TEXT NOT NULL DEFAULT '',
    description              TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_sgr_group ON security_group_rules(group_id);

CREATE TABLE key_pairs (
    key_pair_id TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL,
    name        TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    public_key  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE(account_id, name)
);

CREATE TABLE instances (
    instance_id       TEXT PRIMARY KEY,
    account_id        TEXT NOT NULL,
    instance_type     TEXT NOT NULL,
    vcpu              INTEGER NOT NULL,
    memory_mib        INTEGER NOT NULL,
    image_id          TEXT NOT NULL,
    key_name          TEXT NOT NULL DEFAULT '',
    vpc_id            TEXT NOT NULL REFERENCES vpcs(vpc_id),
    subnet_id         TEXT NOT NULL REFERENCES subnets(subnet_id),
    private_ip        TEXT NOT NULL,
    public_ip         TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL,
    state_reason      TEXT NOT NULL DEFAULT '',
    user_data         TEXT NOT NULL DEFAULT '',
    boot_result       TEXT NOT NULL DEFAULT '{}',   -- JSON result of the sandboxed user-data interpreter
    workload_profile  TEXT NOT NULL DEFAULT 'steady',
    cpu_target        REAL,
    autoscaling_group TEXT NOT NULL DEFAULT '',
    launch_time       TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    terminated_at     TEXT
);
CREATE INDEX idx_instances_account_state ON instances(account_id, state);
CREATE INDEX idx_instances_subnet ON instances(subnet_id);
CREATE INDEX idx_instances_asg ON instances(autoscaling_group);
CREATE UNIQUE INDEX idx_instances_ip ON instances(subnet_id, private_ip) WHERE state <> 'terminated';

CREATE TABLE network_interfaces (
    network_interface_id TEXT PRIMARY KEY,
    account_id           TEXT NOT NULL,
    subnet_id            TEXT NOT NULL REFERENCES subnets(subnet_id),
    vpc_id               TEXT NOT NULL,
    private_ip           TEXT NOT NULL,
    public_ip            TEXT NOT NULL DEFAULT '',
    description          TEXT NOT NULL DEFAULT '',
    instance_id          TEXT REFERENCES instances(instance_id) ON DELETE SET NULL,
    device_index         INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL
);
CREATE INDEX idx_eni_subnet ON network_interfaces(subnet_id);
CREATE INDEX idx_eni_instance ON network_interfaces(instance_id);

-- Security groups attached to an ENI (an instance's groups are those on its primary ENI).
CREATE TABLE eni_security_groups (
    network_interface_id TEXT NOT NULL REFERENCES network_interfaces(network_interface_id) ON DELETE CASCADE,
    group_id             TEXT NOT NULL REFERENCES security_groups(group_id) ON DELETE RESTRICT,
    PRIMARY KEY (network_interface_id, group_id)
);
CREATE INDEX idx_eni_sg_group ON eni_security_groups(group_id);

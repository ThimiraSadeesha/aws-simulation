# CloudSim REST API — v1

Served by the Go backend. Base URL: `http://<host>:<BACKEND_PORT>/api/v1` (default port 8080).
In Docker Compose the frontend proxies `/api/*` to the backend, so the browser uses same-origin `/api/v1`.

> CloudSim is an educational simulator. Every resource here is **simulated** locally; nothing is
> created in a real cloud account.

## Conventions

* JSON in/out, `snake_case` field names, timestamps RFC 3339 UTC.
* IDs follow AWS-style prefixes + 17 lowercase hex chars: `i-`, `vpc-`, `subnet-`, `rtb-`, `igw-`, `nat-`, `sg-`,
  `acl-`, `eni-`, `ami-`, `key-`, `tg-`, `lb-`, `asg-`, `alarm-`. Account ids are 12 digits.
* **Errors**: `{"error":{"code":"ValidationError","message":"...","request_id":"..."}}`.
  Codes: `ValidationError` 400, `Unauthenticated` 401, `AccessDenied` 403, `NotFound` 404,
  `Conflict`/`InvalidStateTransition`/`DependencyViolation` 409, `QuotaExceeded` 429, `RateLimited` 429,
  `EngineUnavailable` 503, `InternalError` 500.
* **Lists**: query params `q` (free-text search over name/id/tags), `sort` (field name), `order` (`asc`|`desc`),
  `page` (1-based), `page_size` (default 20, max 200), plus service-specific filters listed per endpoint.
  Response: `{"items":[...],"total":42,"page":1,"page_size":20}`.
* Every response carries `X-Request-Id`.

## Authentication

Two mechanisms:

1. **Console session** (browser). `POST /auth/login` sets an `HttpOnly; SameSite=Strict` cookie `cs_session`
   and returns a `csrf_token`. Every non-GET request authenticated by cookie must send header
   `X-CSRF-Token: <csrf_token>`. The session principal is the **account root user** (full access to its
   own account) — like signing in with the root user. `GET /auth/me` returns the token again so the SPA can
   recover it after reload.
2. **Access keys** (CLI / programmatic). Header `Authorization: CloudSim <access_key_id>:<secret_access_key>`.
   Access keys belong to IAM users (or temporary STS credentials from assume-role, which also need
   `X-CloudSim-Session-Token`). Requests are authorized by the **IAM policy evaluation engine** — explicit
   Deny > Allow > implicit deny. No CSRF needed.

Platform roles: `admin` (can use `/admin/*`) and `user`. The first registered user becomes `admin`.

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/auth/register` | `{"username","password","email?"}` | `201 {"user":User}` (password ≥ 8 chars, username `^[a-zA-Z0-9_.-]{3,32}$`). Disabled when `ALLOW_REGISTRATION=false` (except the very first user). |
| POST | `/auth/login` | `{"username","password"}` | `200 {"user":User,"csrf_token":"..."}` + cookie |
| POST | `/auth/logout` | – | `204` |
| GET | `/auth/me` | – | `200 {"user":User,"csrf_token":"..."}` |

`User = {"id":1,"username":"alice","email":"","role":"admin","account_id":"123456789012","created_at":"..."}`

Login is rate limited (10/min per IP+username). All API requests are rate limited per principal (default 50 rps, burst 100).

## IAM action names & ARNs

Each endpoint maps to an action `service:Operation` (listed per endpoint) and a resource ARN
`arn:cloudsim:<service>:local:<account_id>:<type>/<id>` (S3: `arn:cloudsim:s3:::<bucket>` and
`arn:cloudsim:s3:::<bucket>/<key>`; IAM: `arn:cloudsim:iam::<account_id>:user/<name>` etc.).
Policies use the familiar JSON grammar (`Version`, `Statement[]` with `Sid`, `Effect`, `Action`/`NotAction`,
`Resource`/`NotResource`, optional `Condition` with `StringEquals`, `StringLike`, `IpAddress` not supported → documented),
wildcards `*` and `?`.

## Dashboard & events

* `GET /dashboard/summary` →
```json
{"resources_total":12,"instances":{"running":2,"stopped":1,"pending":0,"total":3},
 "buckets":2,"objects":10,"storage_bytes":12345,"vpcs":1,"load_balancers":0,"autoscaling_groups":0,
 "alarms":{"ok":1,"alarm":0,"insufficient_data":0},
 "avg_cpu_percent":23.5,"network_in_bytes_5m":1000,"network_out_bytes_5m":800,
 "cpu_series":[{"ts":"...","value":21.2}],
 "recent_events":[Event],"recent_activity":[AuditEntry],
 "health":{"backend":"ok","engine":"ok","database":"ok"},
 "active_labs":[{"lab_id":"ec2-first-instance","title":"...","completed_steps":1,"total_steps":4}]}
```
* `GET /events?resource_id=&type=&page=` → `Event = {"id":1,"ts":"...","resource_id":"i-..","resource_type":"instance","type":"state_changed","message":"pending → running","detail":{}}`
* `GET /audit?action=&principal=&page=` → `AuditEntry = {"id":1,"ts":"...","principal":"root|iam-user/bob","action":"ec2:RunInstances","resource":"arn...","result":"allowed|denied|error","status_code":200,"request_id":"...","source_ip":"..."}`
* WebSocket `GET /ws/events` (cookie auth; first message from client must be `{"csrf_token":"..."}`; also accepts
  `?access_key=` style header auth for CLI). Server pushes `{"type":"event","event":Event}` and
  `{"type":"metrics","samples":[...]}`.

## EC2

`Instance`:
```json
{"instance_id":"i-0abc...","name":"web-1","state":"running","state_reason":"","instance_type":"t3.micro",
 "vcpu":2,"memory_mib":1024,"image_id":"ami-...","key_name":"my-key","vpc_id":"vpc-..","subnet_id":"subnet-..",
 "private_ip":"10.0.1.10","public_ip":"54.12.0.7","private_dns":"ip-10-0-1-10.cloudsim.internal",
 "security_group_ids":["sg-.."],"tags":{"Name":"web-1"},"user_data":"#!/bin/bash\n...",
 "workload_profile":"steady","autoscaling_group":"","launch_time":"...","created_at":"...","updated_at":"..."}
```
| Method | Path | Action | Notes |
|---|---|---|---|
| GET | `/ec2/instances?state=&instance_type=&vpc_id=&tag:<K>=<V>` | `ec2:DescribeInstances` | list |
| POST | `/ec2/instances` | `ec2:RunInstances` | `{"image_id","instance_type","count?":1,"name?","key_name?","subnet_id?","security_group_ids?":[],"associate_public_ip?":bool,"user_data?","tags?":{}}` → `201 {"instances":[Instance]}`. Without `subnet_id` uses the account's default VPC/subnet (created on first use: `172.31.0.0/16`, subnet `172.31.0.0/20`, IGW, default SG). |
| GET | `/ec2/instances/{id}` | `ec2:DescribeInstances` | |
| POST | `/ec2/instances/{id}/start` \| `stop` \| `reboot` \| `terminate` | `ec2:StartInstances`… | `202 Instance` (transitional state). Invalid → `409 InvalidStateTransition`. |
| PUT | `/ec2/instances/{id}/tags` | `ec2:CreateTags` | `{"tags":{"k":"v"}}` replaces all tags |
| PUT | `/ec2/instances/{id}/workload` | `ec2:ModifyInstanceAttribute` | `{"profile":"idle|steady|spiky|stress|custom","cpu_target?":70}` |
| PUT | `/ec2/instances/{id}/security-groups` | `ec2:ModifyInstanceAttribute` | `{"security_group_ids":[...]}` |
| GET | `/ec2/instances/{id}/console-output` | `ec2:GetConsoleOutput` | `{"output":"...","ts":"..."}` — simulated boot log incl. user-data script lines (user-data is **parsed and echoed, not executed** unless the sandbox runner is enabled) |
| GET | `/ec2/instances/{id}/metadata` | `ec2:DescribeInstances` | IMDS-style tree: `{"instance-id":..,"instance-type":..,"ami-id":..,"local-ipv4":..,"public-ipv4":..,"placement/availability-zone":"local-1a","security-groups":..,"tags/instance/Name":..}` |
| GET | `/ec2/instances/{id}/history` | `ec2:DescribeInstances` | state-change events for this instance |
| GET | `/ec2/instance-types` | – | `[{"name":"t3.micro","vcpu":2,"memory_mib":1024,"family":"t3","hourly_price_sim":0.0104}]` |
| GET | `/ec2/images` | `ec2:DescribeImages` | `[{"image_id","name","os","description","architecture"}]` |
| GET/POST | `/ec2/key-pairs` | `ec2:DescribeKeyPairs` / `ec2:CreateKeyPair` | POST `{"name"}` → `201 {"key_pair_id","name","fingerprint","private_key_pem","created_at"}` (private key shown **once**, never stored) |
| DELETE | `/ec2/key-pairs/{name}` | `ec2:DeleteKeyPair` | `204` |

## S3

`Bucket = {"name","region":"local-1","created_at","versioning":"Disabled|Enabled|Suspended","acl":"private|public-read","object_count","size_bytes","has_policy":bool,"lifecycle_rules":[...]}`
`Object = {"key","version_id","is_latest","size","etag","content_type","last_modified","storage_class","metadata":{},"delete_marker":bool}`

| Method | Path | Action | Notes |
|---|---|---|---|
| GET | `/s3/buckets` | `s3:ListAllMyBuckets` | list |
| POST | `/s3/buckets` | `s3:CreateBucket` | `{"name","acl?"}` DNS-compatible names 3–63 chars, globally unique across the platform |
| GET | `/s3/buckets/{bucket}` | `s3:ListBucket` | Bucket |
| DELETE | `/s3/buckets/{bucket}?force=true` | `s3:DeleteBucket` | 409 `BucketNotEmpty` unless empty or force |
| PUT | `/s3/buckets/{bucket}/versioning` | `s3:PutBucketVersioning` | `{"status":"Enabled|Suspended"}` |
| PUT | `/s3/buckets/{bucket}/acl` | `s3:PutBucketAcl` | `{"acl":"private|public-read"}` |
| GET/PUT/DELETE | `/s3/buckets/{bucket}/policy` | `s3:GetBucketPolicy`… | policy JSON document (`Principal` supports `"*"`, `{"CloudSim":"<arn>"}`) — evaluated together with identity policies |
| GET/PUT | `/s3/buckets/{bucket}/lifecycle` | `s3:GetLifecycleConfiguration`… | `{"rules":[{"id","status","prefix","expiration_days?","noncurrent_expiration_days?","transitions?":[{"days","storage_class"}],"abort_incomplete_multipart_days?"}]}`. Applied by a background job every `LIFECYCLE_INTERVAL` (default 60 s) and on `POST /s3/buckets/{bucket}/lifecycle/run`. |
| GET | `/s3/buckets/{bucket}/objects?prefix=&delimiter=&q=&page=` | `s3:ListBucket` | `{"items":[Object],"common_prefixes":["photos/"],...}` |
| GET | `/s3/buckets/{bucket}/versions?prefix=` | `s3:ListBucketVersions` | all versions incl. delete markers |
| PUT | `/s3/buckets/{bucket}/objects/{key...}` | `s3:PutObject` | raw body; `Content-Type`; `x-amz-meta-*` headers → metadata. Max `MAX_OBJECT_SIZE` (default 100 MiB). → `200 Object` |
| POST | `/s3/buckets/{bucket}/upload` | `s3:PutObject` | `multipart/form-data` fields `file` (one or many), `prefix?` → `{"objects":[Object]}` (console uploads) |
| GET | `/s3/buckets/{bucket}/objects/{key...}?version_id=` | `s3:GetObject` | raw bytes, `ETag`, `Content-Type`, `x-amz-meta-*`, `x-amz-version-id` |
| HEAD | same | `s3:GetObject` | headers only |
| GET | `/s3/buckets/{bucket}/object-info/{key...}?version_id=` | `s3:GetObject` | Object JSON |
| DELETE | `/s3/buckets/{bucket}/objects/{key...}?version_id=` | `s3:DeleteObject` | versioned bucket without version_id → delete marker |
| POST | `/s3/buckets/{bucket}/multipart` | `s3:PutObject` | `{"key","content_type?"}` → `{"upload_id"}` |
| PUT | `/s3/buckets/{bucket}/multipart/{upload_id}/parts/{n}` | `s3:PutObject` | raw body (n 1..10000; parts except last ≥ 5 MiB **not enforced**, documented) → `{"part_number","etag","size"}` |
| POST | `/s3/buckets/{bucket}/multipart/{upload_id}/complete` | `s3:PutObject` | `{"parts":[{"part_number","etag"}]}` → Object (ETag `"<md5-of-md5s>-<n>"`) |
| DELETE | `/s3/buckets/{bucket}/multipart/{upload_id}` | `s3:AbortMultipartUpload` | `204` |
| GET | `/s3/buckets/{bucket}/multipart` | `s3:ListBucketMultipartUploads` | in-progress uploads |
| GET | `/s3/stats` | `s3:ListAllMyBuckets` | `{"buckets","objects","versions","size_bytes","by_bucket":[{"name","objects","size_bytes"}],"by_storage_class":{"STANDARD":123}}` |

## VPC / networking

| Resource | Paths | Shape |
|---|---|---|
| VPC | `GET/POST /vpc/vpcs`, `GET/PATCH/DELETE /vpc/vpcs/{id}` | `{"vpc_id","name","cidr_block","is_default","enable_dns_support","enable_dns_hostnames","state":"available","tags","created_at"}`; POST `{"cidr_block","name?","tags?"}` (prefix /16–/28, RFC1918 recommended). Creating a VPC also creates its main route table (`local` route), default network ACL (allow all) and default security group. DELETE fails `409 DependencyViolation` while subnets/IGW/instances exist. |
| Subnet | `GET/POST /vpc/subnets?vpc_id=`, `GET/PATCH/DELETE /vpc/subnets/{id}` | `{"subnet_id","vpc_id","name","cidr_block","availability_zone":"local-1a","map_public_ip_on_launch","available_ip_count","route_table_id","network_acl_id","is_public":bool,"tags"}`. CIDR must be within VPC CIDR and not overlap siblings. `is_public` computed = route table has `0.0.0.0/0 → igw-*`. PATCH `{"name?","map_public_ip_on_launch?"}` |
| Route table | `GET/POST /vpc/route-tables?vpc_id=`, `GET/DELETE /vpc/route-tables/{id}`, `POST /vpc/route-tables/{id}/routes` `{"destination_cidr","target_id"}`, `DELETE /vpc/route-tables/{id}/routes?destination_cidr=`, `POST /vpc/route-tables/{id}/associations` `{"subnet_id"}` | `{"route_table_id","vpc_id","name","is_main","routes":[{"destination_cidr","target_id":"local|igw-..|nat-..","state":"active|blackhole"}],"subnet_ids":[]}` |
| Internet gateway | `GET/POST /vpc/internet-gateways`, `DELETE /vpc/internet-gateways/{id}`, `POST .../{id}/attach` `{"vpc_id"}`, `POST .../{id}/detach` | `{"internet_gateway_id","name","vpc_id":null,"state":"detached|attached"}` |
| NAT gateway | `GET/POST /vpc/nat-gateways`, `DELETE /vpc/nat-gateways/{id}` | `{"nat_gateway_id","name","subnet_id","vpc_id","state":"pending|available|deleting|deleted","public_ip","private_ip"}` POST `{"subnet_id","name?"}` (lifecycle driven by engine) |
| Security group | `GET/POST /vpc/security-groups?vpc_id=`, `GET/DELETE /vpc/security-groups/{id}`, `POST /vpc/security-groups/{id}/rules` `{"direction":"ingress|egress","protocol":"tcp|udp|icmp|-1","from_port","to_port","cidr?","source_security_group_id?","description?"}`, `DELETE /vpc/security-groups/{id}/rules/{rule_id}` | `{"group_id","group_name","description","vpc_id","ingress":[Rule],"egress":[Rule]}`, `Rule={"rule_id","protocol","from_port","to_port","cidr","source_security_group_id","description"}`. New SG gets a default allow-all egress rule. Action `ec2:AuthorizeSecurityGroupIngress` etc. |
| Network ACL | `GET/POST /vpc/network-acls?vpc_id=`, `GET/DELETE /vpc/network-acls/{id}`, `POST .../{id}/entries` `{"rule_number","egress","protocol","from_port","to_port","cidr","action":"allow|deny"}`, `DELETE .../{id}/entries?rule_number=&egress=`, `POST .../{id}/associations` `{"subnet_id"}` | `{"network_acl_id","vpc_id","is_default","entries":[...],"subnet_ids":[]}` (new non-default ACLs deny everything until entries are added) |
| Network interface | `GET/POST /vpc/network-interfaces`, `DELETE /vpc/network-interfaces/{id}` | `{"network_interface_id","subnet_id","vpc_id","private_ip","public_ip","security_group_ids","attachment":{"instance_id","device_index"}|null,"status":"in-use|available","description"}`. Each instance gets a primary ENI automatically. |
| DNS | `GET /vpc/dns/resolve?name=&vpc_id=` | `{"name","answers":[{"type":"A","value":"10.0.1.10","ttl":60}],"source":"vpc-private|route53|public-sim"}` — resolves `ip-*.cloudsim.internal`, instance Name tags `<name>.<vpc_id>.cloudsim.internal` (when DNS hostnames enabled) and Route 53 private zone records |
| Reachability | `POST /vpc/reachability` | `{"source":"i-..|internet|<ip>","destination":"...","protocol":"tcp","port":22}` → engine reachability response (see `engine-protocol.md` §5) |
| Topology | `GET /vpc/vpcs/{id}/topology` | `{"vpc":VPC,"subnets":[Subnet],"route_tables":[..],"internet_gateways":[..],"nat_gateways":[..],"instances":[{"instance_id","name","state","subnet_id","private_ip","public_ip"}],"load_balancers":[..]}` |

## IAM & STS

| Method | Path | Action |
|---|---|---|
| GET/POST | `/iam/users` | `iam:ListUsers` / `iam:CreateUser` `{"user_name","path?":"/","tags?"}` |
| GET/DELETE | `/iam/users/{name}` | `iam:GetUser` (includes `groups`, `attached_policies`, `inline_policies`, `access_keys`) / `iam:DeleteUser` |
| GET/POST | `/iam/users/{name}/access-keys` | POST → `201 {"access_key_id":"AKIA...","secret_access_key":"...","status":"Active"}` (secret shown once; stored hashed) |
| PATCH/DELETE | `/iam/users/{name}/access-keys/{key_id}` | `{"status":"Active|Inactive"}` |
| GET/POST | `/iam/groups`; GET/DELETE `/iam/groups/{name}` | |
| POST/DELETE | `/iam/groups/{name}/members/{user}` | `iam:AddUserToGroup` / `iam:RemoveUserFromGroup` |
| GET/POST | `/iam/roles` | POST `{"role_name","assume_role_policy_document":{…},"description?","max_session_duration?":3600}` |
| GET/DELETE | `/iam/roles/{name}` | |
| GET/POST | `/iam/policies` | managed policies; POST `{"policy_name","description?","policy_document":{…}}`. Built-in read-only AWS-style managed policies exist: `AdministratorAccess`, `ReadOnlyAccess`, `AmazonEC2FullAccess`-style names prefixed `CloudSim` (e.g. `CloudSimEC2FullAccess`, `CloudSimS3ReadOnlyAccess`). |
| GET/PUT/DELETE | `/iam/policies/{arn_or_name}` | PUT creates a new default version |
| POST/DELETE | `/iam/{users|groups|roles}/{name}/attached-policies/{policy_name}` | attach / detach managed |
| PUT/DELETE | `/iam/{users|groups|roles}/{name}/inline-policies/{policy_name}` | body = policy document |
| POST | `/iam/simulate` | `{"principal_arn","action","resource_arn","context?":{}}` → `{"decision":"allowed|explicitDeny|implicitDeny","matched_statements":[{"policy","sid","effect"}]}` |
| POST | `/iam/validate-policy` | `{"policy_document"}` → `{"valid":bool,"errors":[...]}` |
| POST | `/sts/assume-role` | `sts:AssumeRole` `{"role_arn","role_session_name","duration_seconds?"}` → `{"credentials":{"access_key_id":"ASIA...","secret_access_key","session_token","expiration"},"assumed_role_user":{"arn"}}` — caller must be allowed by its own policies AND the role's trust policy |
| GET | `/sts/caller-identity` | `{"account","arn","user_id"}` |

## Elastic Load Balancing

* `GET/POST /elb/target-groups` `{"name","protocol":"HTTP","port":80,"vpc_id","health_check":{"path":"/","interval_s":30,"healthy_threshold":2,"unhealthy_threshold":2}}`
  → `{"target_group_id","arn","name","protocol","port","vpc_id","algorithm":"round_robin|least_outstanding_requests|weighted_random","health_check":{…},"targets":[{"id","port","weight","health":"healthy|unhealthy|unused|initial","reason"}]}`
* `GET/PATCH/DELETE /elb/target-groups/{id}`; `POST /elb/target-groups/{id}/targets` `{"targets":[{"id","port?","weight?"}]}`; `DELETE /elb/target-groups/{id}/targets/{instance_id}`; `GET /elb/target-groups/{id}/health`
* `GET/POST /elb/load-balancers` `{"name","scheme":"internet-facing|internal","subnet_ids":[≥2 recommended, ≥1 required],"security_group_ids":[]}`
  → `{"load_balancer_id","arn","name","dns_name":"<name>-<hash>.elb.cloudsim.local","scheme","state":"provisioning|active|deleting","vpc_id","subnet_ids","security_group_ids","listeners":[{"listener_id","protocol","port","default_target_group_id"}]}`
* `GET/DELETE /elb/load-balancers/{id}`; `POST /elb/load-balancers/{id}/listeners` `{"protocol":"HTTP","port":80,"default_target_group_id"}`; `DELETE /elb/load-balancers/{id}/listeners/{listener_id}`
* `POST /elb/load-balancers/{id}/simulate` `{"requests":1000,"listener_port?":80,"duration_s?":60}` → engine LB simulation result (§7), applied to targets of the listener's target group (raises target CPU).

## Auto Scaling

* `GET/POST /autoscaling/launch-templates` `{"name","image_id","instance_type","security_group_ids?","key_name?","user_data?","workload_profile?"}`
* `GET/POST /autoscaling/groups` `{"name","launch_template","min_size","max_size","desired_capacity","subnet_ids":[...],"target_group_ids?":[],"cooldown_s?":60,"health_check_type?":"EC2|ELB"}`
  → `{"name","arn","launch_template","min_size","max_size","desired_capacity","subnet_ids","target_group_ids","instances":[{"instance_id","state","health"}],"status","created_at"}`
* `GET/PATCH/DELETE /autoscaling/groups/{name}` (PATCH `{"min_size?","max_size?","desired_capacity?"}`; DELETE `?force=true` terminates instances)
* `GET/POST /autoscaling/groups/{name}/policies` `{"name","type":"target_tracking","target_value":50}` or `{"name","type":"step","threshold":80,"comparison":">=","adjustment":1}`; `DELETE .../policies/{policy}`
* `GET /autoscaling/groups/{name}/activities` → `[{"id","ts","description","cause","status":"Successful|Failed|InProgress"}]`
* `POST /autoscaling/groups/{name}/evaluate` – run one scaling evaluation now (the controller also runs every `ASG_INTERVAL`, default 15 s).

## CloudWatch

* `GET /cloudwatch/metrics/list` → `[{"namespace":"CloudSim/EC2","metric_name":"CPUUtilization","dimensions":{"InstanceId":"i-.."}}]`
* `GET /cloudwatch/metrics?namespace=CloudSim/EC2&metric_name=CPUUtilization&dimension=InstanceId:i-..&start=&end=&period=60&stat=Average|Maximum|Minimum|Sum|SampleCount`
  → `{"label","datapoints":[{"ts","value"}]}` (built-in metrics: `CPUUtilization`, `MemoryUtilization`, `NetworkIn`, `NetworkOut`, `DiskReadBytes`, `DiskWriteBytes` per instance; `CloudSim/S3` `BucketSizeBytes`, `NumberOfObjects` per bucket; `CloudSim/AutoScaling` `GroupDesiredCapacity`, `GroupInServiceInstances`).
* `POST /cloudwatch/metrics` `{"namespace","metric_data":[{"metric_name","dimensions?":{},"value","unit?","ts?"}]}` (custom metrics; namespace must not start with `CloudSim/`)
* Alarms: `GET/POST /cloudwatch/alarms` `{"alarm_name","namespace","metric_name","dimensions","statistic":"Average","period":60,"evaluation_periods":1,"threshold":80,"comparison_operator":"GreaterThanThreshold|GreaterThanOrEqualToThreshold|LessThanThreshold|LessThanOrEqualToThreshold","alarm_actions?":["arn:cloudsim:autoscaling:...:policy/..","arn:cloudsim:sns:..."]}` → Alarm `{…,"state":"OK|ALARM|INSUFFICIENT_DATA","state_reason","state_updated_at"}`; `GET/DELETE /cloudwatch/alarms/{name}`; `GET /cloudwatch/alarms/{name}/history`
* Logs: `GET/POST /cloudwatch/log-groups` `{"name","retention_days?"}`, `DELETE /cloudwatch/log-groups/{name}`, `GET /cloudwatch/log-groups/{name}/streams`,
  `POST /cloudwatch/log-groups/{name}/streams/{stream}/events` `{"events":[{"ts?","message"}]}`,
  `GET /cloudwatch/log-groups/{name}/events?filter_pattern=&stream=&start=&end=&limit=` (filter: plain terms all must match, `"quoted phrase"`, `-term` exclusion, `?a ?b` any-of).
  The platform writes instance console/boot logs to `/cloudsim/ec2/console` and audit to `/cloudsim/audit`.

## Training labs

`Lab = {"id":"ec2-first-instance","title","level":"beginner|intermediate|advanced","service","duration_min","description","steps":[{"index":0,"title","instructions_md","hint","validation":{"type":"..."} }]}`

* `GET /labs` → labs + caller progress `{"items":[{...Lab summary,"status":"not_started|in_progress|completed","completed_steps","total_steps"}]}`
* `GET /labs/{id}` → Lab + progress `{"progress":{"status","started_at","completed_at","steps":[{"index","status":"pending|passed|failed","message","checked_at"}]}}`
* `POST /labs/{id}/start` → progress (creates a lab workspace tag `cloudsim:lab=<id>`)
* `POST /labs/{id}/steps/{index}/validate` → `{"passed":bool,"message":"Found running instance i-.. with tag Name=lab-web","details":{}}` — validation queries the **real simulator state** of the caller's account.
* `POST /labs/{id}/reset` → deletes resources tagged `cloudsim:lab=<id>` / named with the lab prefix and resets progress.
* `GET /labs/progress` → summary for dashboard.

## Terminal

WebSocket `GET /ws/terminal` (cookie auth + first message `{"csrf_token"}` ). Client sends `{"type":"input","data":"cloudsim ec2 describe-instances\r"}`
raw keystrokes or `{"type":"exec","command":"..."}`; server sends `{"type":"output","data":"..."}`.
Commands are parsed by a **built-in interpreter** for the `cloudsim` command grammar (same as the CLI) plus
`help`, `clear`, `whoami`, `echo`, `profile use <iam-user>` (switch to acting as an IAM user via one of their
access keys stored for the session). **No shell is ever spawned.** Also available as `POST /terminal/exec` `{"command","profile?"}` → `{"output","exit_code"}`.

## Additional services (simulated, scope documented in docs/services.md)

* SQS: `GET/POST /sqs/queues`, `DELETE /sqs/queues/{name}`, `POST /sqs/queues/{name}/messages` `{"body","delay_seconds?"}`, `GET /sqs/queues/{name}/messages?max=10&visibility_timeout=30` (receive), `DELETE /sqs/queues/{name}/messages/{receipt_handle}`, `POST /sqs/queues/{name}/purge`.
* SNS: `GET/POST /sns/topics`, `DELETE /sns/topics/{name}`, `POST /sns/topics/{name}/subscriptions` `{"protocol":"sqs|log","endpoint"}`, `POST /sns/topics/{name}/publish` `{"message","subject?"}` (fan-out to SQS queues and CloudWatch log groups).
* Secrets Manager: `GET/POST /secrets`, `GET /secrets/{name}` (value redacted), `GET /secrets/{name}/value` (`secretsmanager:GetSecretValue`, audited), `PUT /secrets/{name}`, `DELETE /secrets/{name}`.
* DynamoDB: `GET/POST /dynamodb/tables` `{"table_name","partition_key":{"name","type":"S|N"},"sort_key?":{…}}`, `DELETE /dynamodb/tables/{name}`, `PUT /dynamodb/tables/{name}/items` (item JSON), `GET /dynamodb/tables/{name}/items?pk=&sk=`, `POST /dynamodb/tables/{name}/query` `{"pk","sk_begins_with?"}`, `GET /dynamodb/tables/{name}/scan?limit=`, `DELETE /dynamodb/tables/{name}/items?pk=&sk=`.
* Route 53: `GET/POST /route53/zones` `{"name","private":bool,"vpc_id?"}`, `DELETE /route53/zones/{id}`, `GET/POST /route53/zones/{id}/records` `{"name","type":"A|CNAME|TXT","ttl","values":[]}`, `DELETE /route53/zones/{id}/records?name=&type=`.
* Lambda / RDS / ECS / ECR / EKS / CloudFormation: listed in the console as “coming later” — **not implemented** in this release (see docs/roadmap.md). Endpoints return `501 NotImplemented`.

## Admin (platform role `admin`)

* `GET /admin/users`, `PATCH /admin/users/{id}` `{"role"}`
* `GET /admin/engine` → engine `/v1/stats` + quotas
* `POST /admin/backups` → `{"file":"cloudsim-20261115T100000Z.db","size"}` (SQLite `VACUUM INTO`), `GET /admin/backups`, `POST /admin/restore` `{"file"}` (validated with `PRAGMA integrity_check` before swap; backend reloads)
* `GET /admin/integrity` → `{"integrity_check":"ok","foreign_key_violations":[]}`
* `POST /admin/faults` / `GET /admin/faults` / `DELETE /admin/faults/{id}` → engine fault injection (§10)

## Health

* `GET /healthz` (no auth, not under `/api/v1`) → `{"status":"ok","database":"ok","engine":"ok|unavailable","version"}`
